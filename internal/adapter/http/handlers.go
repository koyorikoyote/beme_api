package httphandler

import (
	"context"
	"encoding/json"
	"fmt"
	stdhttp "net/http"
	"time"

	"github.com/beme/beme/internal/adapter/chat"
	"github.com/beme/beme/internal/usecase"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

var rateLimitRejections = promauto.NewCounter(prometheus.CounterOpts{
	Name: "beme_rate_limit_rejections_total",
	Help: "Total number of requests rejected by the rate limiter.",
})

// Handlers holds all HTTP handler dependencies.
type Handlers struct {
	rateLimiter  *usecase.RateLimiterUseCase
	chatProvider *chat.RESTChatProvider
	redisClient  *goredis.Client
	vllmEndpoint string
	logger       *zap.Logger
}

// NewHandlers constructs a Handlers instance.
func NewHandlers(
	rateLimiter *usecase.RateLimiterUseCase,
	chatProvider *chat.RESTChatProvider,
	redisClient *goredis.Client,
	vllmEndpoint string,
	logger *zap.Logger,
) *Handlers {
	return &Handlers{
		rateLimiter:  rateLimiter,
		chatProvider: chatProvider,
		redisClient:  redisClient,
		vllmEndpoint: vllmEndpoint,
		logger:       logger,
	}
}

// RegisterRoutes registers all routes on the provided mux.
func (h *Handlers) RegisterRoutes(mux *stdhttp.ServeMux) {
	mux.HandleFunc("POST /api/chat", h.handleChat)
	mux.HandleFunc("GET /health", h.handleHealth)
	mux.HandleFunc("GET /ready", h.handleReady)
	mux.Handle("GET /metrics", promhttp.Handler())
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w stdhttp.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// clientID extracts a client identifier from X-Forwarded-For or RemoteAddr.
func clientID(r *stdhttp.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	return r.RemoteAddr
}

// handleChat handles POST /api/chat.
func (h *Handlers) handleChat(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	ctx := r.Context()
	id := clientID(r)

	allowed, headers, err := h.rateLimiter.Allow(ctx, id)
	if err != nil {
		h.logger.Error("rate limiter error", zap.Error(err))
		writeJSON(w, stdhttp.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	if !allowed {
		rateLimitRejections.Inc()
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", headers.Limit))
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", headers.Remaining))
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", headers.Reset))
		writeJSON(w, stdhttp.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		return
	}

	var msg chat.IncomingMessage
	if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
		writeJSON(w, stdhttp.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	if msg.ViewerID == "" || msg.Content == "" {
		writeJSON(w, stdhttp.StatusBadRequest, map[string]string{"error": "viewer_id and content are required"})
		return
	}

	if err := h.chatProvider.Submit(msg); err != nil {
		h.logger.Error("failed to submit message", zap.Error(err))
		writeJSON(w, stdhttp.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	writeJSON(w, stdhttp.StatusAccepted, map[string]string{"status": "accepted"})
}

// handleHealth handles GET /health — liveness probe.
func (h *Handlers) handleHealth(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
	writeJSON(w, stdhttp.StatusOK, map[string]string{"status": "ok"})
}

// handleReady handles GET /ready — readiness probe.
func (h *Handlers) handleReady(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	ctx := r.Context()

	redisStatus := "ok"
	if err := h.redisClient.Ping(ctx).Err(); err != nil {
		h.logger.Warn("redis ping failed", zap.Error(err))
		redisStatus = "error"
	}

	vllmStatus := "ok"
	vllmCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := stdhttp.NewRequestWithContext(vllmCtx, stdhttp.MethodGet, h.vllmEndpoint+"/health", nil)
	if err != nil {
		vllmStatus = "error"
	} else {
		resp, err := stdhttp.DefaultClient.Do(req)
		if err != nil || resp.StatusCode >= 500 {
			h.logger.Warn("vllm health check failed", zap.Error(err))
			vllmStatus = "error"
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
	}

	body := map[string]string{
		"redis": redisStatus,
		"vllm":  vllmStatus,
	}

	if redisStatus == "ok" && vllmStatus == "ok" {
		body["status"] = "ready"
		writeJSON(w, stdhttp.StatusOK, body)
	} else {
		body["status"] = "degraded"
		writeJSON(w, stdhttp.StatusServiceUnavailable, body)
	}
}
