package usecase

import (
	"context"
	"time"

	"github.com/beme/beme/internal/domain"
)

// ProfileRepository is consumed by ExperienceMatcherUseCase.
type ProfileRepository interface {
	GetProfile(ctx context.Context, viewerID string) (*domain.ViewerProfile, error)
	UpsertProfile(ctx context.Context, profile *domain.ViewerProfile) error
}

// LLMClient is consumed by BionicBatcherUseCase.
type LLMClient interface {
	InferBatch(ctx context.Context, prompt string) ([]domain.TipCard, error)
}

// SemanticCacheRepository is consumed by SemanticCacheUseCase.
type SemanticCacheRepository interface {
	FindSimilar(ctx context.Context, embedding []float32, threshold float64) ([]domain.TipCard, error)
	Store(ctx context.Context, embedding []float32, cards []domain.TipCard, ttl time.Duration) error
}

// EmbeddingClient is consumed by SemanticCacheUseCase.
type EmbeddingClient interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// RateLimiter is consumed by HTTP middleware.
type RateLimiter interface {
	Allow(ctx context.Context, clientID string) (allowed bool, headers RateLimitHeaders, err error)
}

// ChatProvider is consumed by the ingestion layer.
type ChatProvider interface {
	Connect(ctx context.Context) error
	Receive(ctx context.Context) (<-chan domain.ChatMessage, error)
	Disconnect(ctx context.Context) error
}

// HUDBroadcaster is consumed by BionicBatcherUseCase.
type HUDBroadcaster interface {
	Broadcast(ctx context.Context, cards []domain.TipCard) error
}

// RateLimitHeaders holds the rate limit metadata returned to callers.
type RateLimitHeaders struct {
	Limit     int
	Remaining int
	Reset     int64 // Unix timestamp
}
