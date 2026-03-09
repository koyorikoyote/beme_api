package vllm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// embeddingRequest is the JSON body sent to the OpenAI-compatible embeddings API.
type embeddingRequest struct {
	Input string `json:"input"`
	Model string `json:"model"`
}

// embeddingResponse is the top-level JSON response from the embeddings API.
type embeddingResponse struct {
	Data []embeddingData `json:"data"`
}

type embeddingData struct {
	Embedding []float64 `json:"embedding"`
}

// EmbeddingClient is an HTTP client for the OpenAI-compatible embeddings API.
// It implements usecase.EmbeddingClient.
type EmbeddingClient struct {
	endpoint   string
	modelName  string
	timeout    time.Duration
	httpClient *http.Client
	logger     *zap.Logger
}

// NewEmbeddingClient constructs an EmbeddingClient.
func NewEmbeddingClient(endpoint, modelName string, timeout time.Duration, logger *zap.Logger) *EmbeddingClient {
	return &EmbeddingClient{
		endpoint:   endpoint,
		modelName:  modelName,
		timeout:    timeout,
		httpClient: &http.Client{},
		logger:     logger,
	}
}

// Embed sends text to the embeddings endpoint and returns a []float32 vector.
// It implements usecase.EmbeddingClient.
func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float32, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	reqBody := embeddingRequest{
		Input: text,
		Model: c.modelName,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("embedding: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, c.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("embedding: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if callCtx.Err() != nil {
			return nil, fmt.Errorf("embedding: request timed out: %w", callCtx.Err())
		}
		return nil, fmt.Errorf("embedding: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding: unexpected status %d", resp.StatusCode)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("embedding: read response body: %w", err)
	}

	var apiResp embeddingResponse
	if err := json.Unmarshal(respBytes, &apiResp); err != nil {
		return nil, fmt.Errorf("embedding: parse response: %w", err)
	}

	if len(apiResp.Data) == 0 {
		return nil, fmt.Errorf("embedding: no data in response")
	}

	raw := apiResp.Data[0].Embedding
	vector := make([]float32, len(raw))
	for i, v := range raw {
		vector[i] = float32(v)
	}

	c.logger.Debug("embedding computed", zap.Int("dim", len(vector)))
	return vector, nil
}
