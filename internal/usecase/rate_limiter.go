package usecase

import (
	"context"

	"go.uber.org/zap"
)

// RateLimiterUseCase wraps the RateLimiter interface and applies fail-open
// semantics: if the underlying limiter returns an error (e.g. Redis failure),
// the request is allowed and the error is logged rather than propagated.
type RateLimiterUseCase struct {
	limiter RateLimiter
	logger  *zap.Logger
}

// NewRateLimiterUseCase constructs a RateLimiterUseCase.
func NewRateLimiterUseCase(limiter RateLimiter, logger *zap.Logger) *RateLimiterUseCase {
	return &RateLimiterUseCase{
		limiter: limiter,
		logger:  logger,
	}
}

// Allow checks whether the given clientID is within its rate limit.
// On success it returns the limiter's allow/deny decision and headers.
// On error it fails open: returns (true, default headers, nil) and logs the error.
func (r *RateLimiterUseCase) Allow(ctx context.Context, clientID string) (bool, RateLimitHeaders, error) {
	allowed, headers, err := r.limiter.Allow(ctx, clientID)
	if err != nil {
		r.logger.Error("rate limiter error, failing open", zap.String("client_id", clientID), zap.Error(err))
		return true, RateLimitHeaders{}, nil
	}
	return allowed, headers, nil
}
