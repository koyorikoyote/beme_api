package redis

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/beme/beme/internal/domain"
	"github.com/beme/beme/internal/usecase"
	goredis "github.com/redis/go-redis/v9"
)

// Ensure RedisSemanticCacheRepo implements the SemanticCacheRepository interface.
var _ usecase.SemanticCacheRepository = (*RedisSemanticCacheRepo)(nil)

// RedisSemanticCacheRepo stores and retrieves cached LLM responses using Redis VSS (vector similarity search).
type RedisSemanticCacheRepo struct {
	client *goredis.Client
}

// NewRedisSemanticCacheRepo creates a new RedisSemanticCacheRepo with the given Redis client.
func NewRedisSemanticCacheRepo(client *goredis.Client) *RedisSemanticCacheRepo {
	return &RedisSemanticCacheRepo{client: client}
}

// EnsureIndex creates the Redis VSS index idx:prompt_cache if it does not already exist.
// It is safe to call multiple times; an "Index already exists" error is silently ignored.
func (r *RedisSemanticCacheRepo) EnsureIndex(ctx context.Context) error {
	err := r.client.Do(ctx,
		"FT.CREATE", "idx:prompt_cache",
		"ON", "HASH",
		"PREFIX", "1", "cache:prompt:",
		"SCHEMA",
		"embedding", "VECTOR", "HNSW", "6",
		"TYPE", "FLOAT32",
		"DIM", "768",
		"DISTANCE_METRIC", "COSINE",
		"response", "TEXT",
		"created_at", "NUMERIC", "SORTABLE",
	).Err()

	if err != nil && !strings.Contains(err.Error(), "Index already exists") {
		return fmt.Errorf("redis: create index idx:prompt_cache: %w", err)
	}
	return nil
}

// embeddingToBytes converts a []float32 slice to a little-endian byte slice.
func embeddingToBytes(embedding []float32) []byte {
	buf := make([]byte, len(embedding)*4)
	for i, v := range embedding {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// embeddingKey returns the Redis key for a given embedding using sha256 of the bytes.
func embeddingKey(embBytes []byte) string {
	hash := sha256.Sum256(embBytes)
	return fmt.Sprintf("cache:prompt:%x", hash)
}

// FindSimilar searches for a cached response whose embedding is within the given cosine distance threshold.
// Returns an empty slice (no error) on cache miss.
func (r *RedisSemanticCacheRepo) FindSimilar(ctx context.Context, embedding []float32, threshold float64) ([]domain.TipCard, error) {
	embBytes := embeddingToBytes(embedding)

	// FT.SEARCH with KNN vector query
	raw, err := r.client.Do(ctx,
		"FT.SEARCH", "idx:prompt_cache",
		"*=>[KNN 1 @embedding $vec AS score]",
		"PARAMS", "2", "vec", embBytes,
		"SORTBY", "score",
		"DIALECT", "2",
	).Result()

	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return []domain.TipCard{}, nil
		}
		return nil, fmt.Errorf("redis: FT.SEARCH idx:prompt_cache: %w", err)
	}

	// Redis Stack DIALECT 2 returns a map[interface{}]interface{} with "total_results" and "results" keys.
	resultMap, ok := raw.(map[interface{}]interface{})
	if !ok {
		// Fallback: older Redis returns []interface{} — handle that too.
		return r.parseLegacySearchResult(raw, threshold)
	}

	totalRaw, ok := resultMap["total_results"]
	if !ok {
		return []domain.TipCard{}, nil
	}
	total, _ := totalRaw.(int64)
	if total == 0 {
		return []domain.TipCard{}, nil
	}

	results, ok := resultMap["results"].([]interface{})
	if !ok || len(results) == 0 {
		return []domain.TipCard{}, nil
	}

	// Each result is a map with "id", "extra_attributes" keys.
	firstResult, ok := results[0].(map[interface{}]interface{})
	if !ok {
		return []domain.TipCard{}, nil
	}

	attrs, ok := firstResult["extra_attributes"].(map[interface{}]interface{})
	if !ok {
		return []domain.TipCard{}, nil
	}

	scoreStr, _ := attrs["score"].(string)
	responseStr, _ := attrs["response"].(string)
	if scoreStr == "" || responseStr == "" {
		return []domain.TipCard{}, nil
	}

	var score float64
	if _, err := fmt.Sscanf(scoreStr, "%f", &score); err != nil {
		return []domain.TipCard{}, nil
	}
	if score > threshold {
		return []domain.TipCard{}, nil
	}

	var cards []domain.TipCard
	if err := json.Unmarshal([]byte(responseStr), &cards); err != nil {
		return nil, fmt.Errorf("redis: unmarshal cached response: %w", err)
	}
	return cards, nil
}

// parseLegacySearchResult handles the older []interface{} FT.SEARCH response format.
func (r *RedisSemanticCacheRepo) parseLegacySearchResult(raw interface{}, threshold float64) ([]domain.TipCard, error) {
	result, ok := raw.([]interface{})
	if !ok || len(result) < 3 {
		return []domain.TipCard{}, nil
	}

	count, _ := result[0].(int64)
	if count == 0 {
		return []domain.TipCard{}, nil
	}

	fields, ok := result[2].([]interface{})
	if !ok {
		return []domain.TipCard{}, nil
	}

	fieldMap := make(map[string]string)
	for i := 0; i+1 < len(fields); i += 2 {
		k, ok1 := fields[i].(string)
		v, ok2 := fields[i+1].(string)
		if ok1 && ok2 {
			fieldMap[k] = v
		}
	}

	scoreStr, hasScore := fieldMap["score"]
	responseStr, hasResponse := fieldMap["response"]
	if !hasScore || !hasResponse {
		return []domain.TipCard{}, nil
	}

	var score float64
	if _, err := fmt.Sscanf(scoreStr, "%f", &score); err != nil {
		return []domain.TipCard{}, nil
	}
	if score > threshold {
		return []domain.TipCard{}, nil
	}

	var cards []domain.TipCard
	if err := json.Unmarshal([]byte(responseStr), &cards); err != nil {
		return nil, fmt.Errorf("redis: unmarshal cached response: %w", err)
	}
	return cards, nil
}

// Store saves the embedding and serialized TipCards in Redis with the given TTL.
// Key: cache:prompt:{sha256_hex_of_embedding_bytes}
func (r *RedisSemanticCacheRepo) Store(ctx context.Context, embedding []float32, cards []domain.TipCard, ttl time.Duration) error {
	embBytes := embeddingToBytes(embedding)
	key := embeddingKey(embBytes)

	responseJSON, err := json.Marshal(cards)
	if err != nil {
		return fmt.Errorf("redis: marshal tip cards: %w", err)
	}

	if err := r.client.HSet(ctx, key,
		"embedding", embBytes,
		"response", string(responseJSON),
		"created_at", time.Now().Unix(),
	).Err(); err != nil {
		return fmt.Errorf("redis: HSET %s: %w", key, err)
	}

	if err := r.client.Expire(ctx, key, ttl).Err(); err != nil {
		return fmt.Errorf("redis: EXPIRE %s: %w", key, err)
	}

	return nil
}
