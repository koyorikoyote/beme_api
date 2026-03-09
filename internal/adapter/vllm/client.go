package vllm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/beme/beme/internal/domain"
	"github.com/beme/beme/pkg/sanitize"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

// Ensure VLLMClient implements usecase.LLMClient at compile time.
// (Import avoided to prevent circular deps; the interface is checked in tests.)

// vllmRequestDuration is the Prometheus histogram for vLLM request latency.
var vllmRequestDuration = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "beme_vllm_request_duration_seconds",
		Help:    "Duration of vLLM inference requests in seconds.",
		Buckets: prometheus.DefBuckets,
	},
	[]string{"status"},
)

// openAIRequest is the JSON body sent to the OpenAI-compatible API.
type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openAIResponse is the top-level JSON response from the API.
type openAIResponse struct {
	Choices []openAIChoice `json:"choices"`
}

type openAIChoice struct {
	Message openAIMessage `json:"message"`
}

// tipCardRaw is the raw JSON shape of a TipCard as returned by the LLM.
type tipCardRaw struct {
	TipType        string  `json:"tip_type"`
	Message        string  `json:"message"`
	ExpertHandle   string  `json:"expert_handle"`
	SentimentScore float64 `json:"sentiment_score"`
	BatchID        string  `json:"batch_id"`
}

// VLLMClient is an HTTP client for the vLLM OpenAI-compatible inference API.
type VLLMClient struct {
	endpoint   string
	modelName  string
	timeout    time.Duration
	httpClient *http.Client
	logger     *zap.Logger
}

// NewVLLMClient constructs a VLLMClient.
func NewVLLMClient(endpoint, modelName string, timeout time.Duration, logger *zap.Logger) *VLLMClient {
	return &VLLMClient{
		endpoint:   endpoint,
		modelName:  modelName,
		timeout:    timeout,
		httpClient: &http.Client{},
		logger:     logger,
	}
}

// InferBatch sends a single inference request to the vLLM engine and returns
// parsed, validated, and sanitized TipCard objects.
// It implements usecase.LLMClient.
func (c *VLLMClient) InferBatch(ctx context.Context, prompt string) ([]domain.TipCard, error) {
	// Enforce per-call timeout.
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	start := time.Now()

	cards, err := c.doInfer(callCtx, prompt)
	duration := time.Since(start).Seconds()

	if err != nil {
		vllmRequestDuration.WithLabelValues("error").Observe(duration)
		c.logger.Error("vllm inference failed", zap.Error(err))
		return nil, err
	}

	vllmRequestDuration.WithLabelValues("success").Observe(duration)
	return cards, nil
}

// doInfer performs the actual HTTP call and response processing.
func (c *VLLMClient) doInfer(ctx context.Context, prompt string) ([]domain.TipCard, error) {
	// Build request body.
	reqBody := openAIRequest{
		Model: c.modelName,
		Messages: []openAIMessage{
			{Role: "system", Content: "You are a JSON API. You only output valid JSON arrays. Never include explanations, markdown, or any text outside the JSON array."},
			{Role: "user", Content: prompt},
		},
		Temperature: 0.3,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("vllm: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("vllm: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("vllm: %w: %w", domain.ErrLLMTimeout, ctx.Err())
		}
		return nil, fmt.Errorf("vllm: %w: %s", domain.ErrLLMUnavailable, err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("vllm: %w: status %d", domain.ErrLLMUnavailable, resp.StatusCode)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("vllm: read response body: %w", err)
	}

	// Parse the OpenAI-compatible envelope.
	var apiResp openAIResponse
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("vllm: %w: %s", domain.ErrLLMResponseInvalid, err.Error())
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("vllm: %w: no choices in response", domain.ErrLLMResponseInvalid)
	}

	content := apiResp.Choices[0].Message.Content

	// Strip markdown code fences if the model wrapped the JSON (e.g. ```json ... ```)
	content = stripCodeFences(content)

	// Fallback: extract the first JSON array found in the content.
	if !strings.HasPrefix(content, "[") {
		if start := strings.Index(content, "["); start != -1 {
			if end := strings.LastIndex(content, "]"); end > start {
				content = content[start : end+1]
			}
		}
	}

	// Parse the content as a JSON array of TipCard objects.
	var rawCards []tipCardRaw
	if err := json.Unmarshal([]byte(content), &rawCards); err != nil {
		c.logger.Warn("vllm: raw LLM content that failed parsing", zap.String("content", content))
		return nil, fmt.Errorf("vllm: %w: content is not a valid TipCard array: %s", domain.ErrLLMResponseInvalid, err.Error())
	}

	return c.validateAndSanitize(rawCards)
}

// validateAndSanitize validates each raw card, sanitizes the message field,
// and returns only the cards that pass both checks.
// If ALL cards are discarded by sanitization, it returns (nil, ErrSanitizationDiscard).
func (c *VLLMClient) validateAndSanitize(rawCards []tipCardRaw) ([]domain.TipCard, error) {
	var cards []domain.TipCard
	discardedBySanitization := 0

	for _, raw := range rawCards {
		// Structural validation.
		if raw.TipType == "" || raw.Message == "" || raw.ExpertHandle == "" {
			c.logger.Warn("vllm: skipping card with empty required fields",
				zap.String("tip_type", raw.TipType),
				zap.String("expert_handle", raw.ExpertHandle),
			)
			continue
		}
		if raw.SentimentScore < 0.0 || raw.SentimentScore > 1.0 {
			c.logger.Warn("vllm: skipping card with out-of-range sentiment_score",
				zap.Float64("sentiment_score", raw.SentimentScore),
			)
			continue
		}

		// Sanitize the message field.
		sanitized, err := sanitize.SanitizeWithThreshold(raw.Message, 0.5)
		if err != nil {
			if errors.Is(err, domain.ErrSanitizationDiscard) {
				c.logger.Warn("vllm: card message discarded by sanitization threshold",
					zap.String("batch_id", raw.BatchID),
				)
				discardedBySanitization++
				continue
			}
			return nil, fmt.Errorf("vllm: sanitize: %w", err)
		}

		cards = append(cards, domain.TipCard{
			TipType:        raw.TipType,
			Message:        sanitized,
			ExpertHandle:   raw.ExpertHandle,
			SentimentScore: raw.SentimentScore,
			BatchID:        raw.BatchID,
		})
	}

	// If every card was discarded by sanitization, surface the discard error.
	if len(rawCards) > 0 && len(cards) == 0 && discardedBySanitization == len(rawCards) {
		return nil, domain.ErrSanitizationDiscard
	}

	return cards, nil
}

// stripCodeFences removes markdown code fences that some models wrap JSON in.
// e.g. ```json\n[...]\n``` → [...]
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Remove opening fence (```json or just ```)
		if idx := strings.Index(s, "\n"); idx != -1 {
			s = s[idx+1:]
		}
		// Remove closing fence
		if idx := strings.LastIndex(s, "```"); idx != -1 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	return s
}
