package usecase

import (
	"context"
	"time"

	"github.com/beme/beme/internal/domain"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

var (
	cacheHitsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "beme_semantic_cache_hits_total",
		Help: "Total number of semantic cache hits.",
	})
	cacheMissesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "beme_semantic_cache_misses_total",
		Help: "Total number of semantic cache misses.",
	})
)

func init() {
	prometheus.MustRegister(cacheHitsTotal, cacheMissesTotal)
}

// SemanticCacheUseCase computes prompt embeddings, checks the semantic cache for
// similar prior responses, and stores new responses for future reuse.
type SemanticCacheUseCase struct {
	repo      SemanticCacheRepository
	embedder  EmbeddingClient
	threshold float64
	ttl       time.Duration
	logger    *zap.Logger
}

// NewSemanticCacheUseCase constructs a SemanticCacheUseCase.
func NewSemanticCacheUseCase(
	repo SemanticCacheRepository,
	embedder EmbeddingClient,
	threshold float64,
	ttl time.Duration,
	logger *zap.Logger,
) *SemanticCacheUseCase {
	return &SemanticCacheUseCase{
		repo:      repo,
		embedder:  embedder,
		threshold: threshold,
		ttl:       ttl,
		logger:    logger,
	}
}

// Check computes an embedding for the given prompt and searches the cache for a
// similar prior response. Returns (cards, true, nil) on a hit, or (nil, false, nil)
// on a miss. Cache errors are logged as warnings and treated as misses — they are
// never propagated to the caller.
func (s *SemanticCacheUseCase) Check(ctx context.Context, prompt string) ([]domain.TipCard, bool, error) {
	embedding, err := s.embedder.Embed(ctx, prompt)
	if err != nil {
		s.logger.Warn("semantic cache: embedding failed, treating as miss", zap.Error(err))
		cacheMissesTotal.Inc()
		return nil, false, nil
	}

	cards, err := s.repo.FindSimilar(ctx, embedding, s.threshold)
	if err != nil {
		s.logger.Warn("semantic cache: FindSimilar failed, treating as miss", zap.Error(err))
		cacheMissesTotal.Inc()
		return nil, false, nil
	}

	if len(cards) > 0 {
		cacheHitsTotal.Inc()
		return cards, true, nil
	}

	cacheMissesTotal.Inc()
	return nil, false, nil
}

// Store computes an embedding for the given prompt and persists the cards in the
// cache with the configured TTL. Returns an error if embedding or storage fails.
func (s *SemanticCacheUseCase) Store(ctx context.Context, prompt string, cards []domain.TipCard) error {
	embedding, err := s.embedder.Embed(ctx, prompt)
	if err != nil {
		return err
	}

	return s.repo.Store(ctx, embedding, cards, s.ttl)
}
