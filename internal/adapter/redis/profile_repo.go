package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/beme/beme/internal/domain"
	"github.com/beme/beme/internal/usecase"
	goredis "github.com/redis/go-redis/v9"
)

// Ensure RedisProfileRepo implements the ProfileRepository interface.
var _ usecase.ProfileRepository = (*RedisProfileRepo)(nil)

// RedisProfileRepo stores and retrieves viewer profiles using RedisJSON and RediSearch.
type RedisProfileRepo struct {
	client *goredis.Client
}

// NewRedisProfileRepo creates a new RedisProfileRepo with the given Redis client.
func NewRedisProfileRepo(client *goredis.Client) *RedisProfileRepo {
	return &RedisProfileRepo{client: client}
}

// EnsureIndex creates the RediSearch index idx:viewers if it does not already exist.
// It is safe to call multiple times; an "Index already exists" error is silently ignored.
func (r *RedisProfileRepo) EnsureIndex(ctx context.Context) error {
	err := r.client.Do(ctx,
		"FT.CREATE", "idx:viewers",
		"ON", "JSON",
		"PREFIX", "1", "viewer:",
		"SCHEMA",
		"$.viewer_id", "AS", "viewer_id", "TAG",
		"$.contribution_score", "AS", "contribution_score", "NUMERIC", "SORTABLE",
		"$.expertise_tags[*]", "AS", "expertise_tags", "TAG",
	).Err()

	if err != nil && !strings.Contains(err.Error(), "Index already exists") {
		return fmt.Errorf("redis: create index idx:viewers: %w", err)
	}
	return nil
}

// GetProfile retrieves a viewer profile by viewer ID.
// Returns domain.ErrProfileNotFound when the key does not exist.
func (r *RedisProfileRepo) GetProfile(ctx context.Context, viewerID string) (*domain.ViewerProfile, error) {
	key := "viewer:" + viewerID

	result, err := r.client.Do(ctx, "JSON.GET", key, "$").Text()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return nil, domain.ErrProfileNotFound
		}
		return nil, fmt.Errorf("redis: JSON.GET %s: %w", key, err)
	}

	// RedisJSON returns a JSON array wrapping the root document when using "$" path.
	var profiles []domain.ViewerProfile
	if err := json.Unmarshal([]byte(result), &profiles); err != nil {
		return nil, fmt.Errorf("redis: unmarshal profile %s: %w", key, err)
	}
	if len(profiles) == 0 {
		return nil, domain.ErrProfileNotFound
	}

	return &profiles[0], nil
}

// UpsertProfile serializes the profile to JSON and stores it at viewer:{viewer_id}.
func (r *RedisProfileRepo) UpsertProfile(ctx context.Context, profile *domain.ViewerProfile) error {
	key := "viewer:" + profile.ViewerID

	data, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("redis: marshal profile %s: %w", key, err)
	}

	if err := r.client.Do(ctx, "JSON.SET", key, "$", string(data)).Err(); err != nil {
		return fmt.Errorf("redis: JSON.SET %s: %w", key, err)
	}
	return nil
}
