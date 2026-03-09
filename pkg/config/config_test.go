package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setEnv sets multiple env vars and returns a cleanup function.
func setEnv(t *testing.T, pairs map[string]string) {
	t.Helper()
	for k, v := range pairs {
		t.Setenv(k, v)
	}
}

func TestLoad_AllDefaults(t *testing.T) {
	setEnv(t, map[string]string{
		"REDIS_URL":     "redis://localhost:6379",
		"VLLM_ENDPOINT": "http://localhost:8000",
	})

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "redis://localhost:6379", cfg.RedisURL)
	assert.Equal(t, "http://localhost:8000", cfg.VLLMEndpoint)

	// Optional defaults
	assert.Equal(t, "", cfg.RedisPassword)
	assert.Equal(t, 0, cfg.RedisDB)
	assert.Equal(t, "default", cfg.VLLMModelName)
	assert.Equal(t, 5, cfg.VLLMTimeoutSeconds)
	assert.Equal(t, 75, cfg.PriorityThreshold)
	assert.Equal(t, 2, cfg.BatchWindowSeconds)
	assert.Equal(t, 100, cfg.RateLimitRPS)
	assert.Equal(t, 8080, cfg.HTTPPort)
	assert.Equal(t, 8081, cfg.WSPort)
	assert.Equal(t, 300, cfg.CacheTTLSeconds)
	assert.InDelta(t, 0.05, cfg.CacheSimilarityThreshold, 1e-9)
	assert.Equal(t, 30, cfg.WSPingIntervalSeconds)
	assert.Equal(t, 10, cfg.WSPongTimeoutSeconds)
	assert.Equal(t, 256, cfg.WSSendBufferSize)
	assert.Equal(t, "info", cfg.LogLevel)
}

func TestLoad_MissingRedisURL(t *testing.T) {
	// Ensure REDIS_URL is absent; VLLM_ENDPOINT is present.
	os.Unsetenv("REDIS_URL")
	t.Setenv("VLLM_ENDPOINT", "http://localhost:8000")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "REDIS_URL")
}

func TestLoad_MissingVLLMEndpoint(t *testing.T) {
	t.Setenv("REDIS_URL", "redis://localhost:6379")
	os.Unsetenv("VLLM_ENDPOINT")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "VLLM_ENDPOINT")
}

func TestLoad_OverridesApplied(t *testing.T) {
	setEnv(t, map[string]string{
		"REDIS_URL":                  "redis://prod:6379",
		"REDIS_PASSWORD":             "secret",
		"REDIS_DB":                   "2",
		"VLLM_ENDPOINT":              "http://vllm:8000",
		"VLLM_MODEL_NAME":            "llama3",
		"VLLM_TIMEOUT_SECONDS":       "10",
		"PRIORITY_THRESHOLD":         "50",
		"BATCH_WINDOW_SECONDS":       "4",
		"RATE_LIMIT_RPS":             "200",
		"HTTP_PORT":                  "9090",
		"WS_PORT":                    "9091",
		"CACHE_TTL_SECONDS":          "600",
		"CACHE_SIMILARITY_THRESHOLD": "0.1",
		"WS_PING_INTERVAL_SECONDS":   "60",
		"WS_PONG_TIMEOUT_SECONDS":    "20",
		"WS_SEND_BUFFER_SIZE":        "512",
		"LOG_LEVEL":                  "debug",
	})

	cfg, err := Load()
	require.NoError(t, err)

	assert.Equal(t, "redis://prod:6379", cfg.RedisURL)
	assert.Equal(t, "secret", cfg.RedisPassword)
	assert.Equal(t, 2, cfg.RedisDB)
	assert.Equal(t, "http://vllm:8000", cfg.VLLMEndpoint)
	assert.Equal(t, "llama3", cfg.VLLMModelName)
	assert.Equal(t, 10, cfg.VLLMTimeoutSeconds)
	assert.Equal(t, 50, cfg.PriorityThreshold)
	assert.Equal(t, 4, cfg.BatchWindowSeconds)
	assert.Equal(t, 200, cfg.RateLimitRPS)
	assert.Equal(t, 9090, cfg.HTTPPort)
	assert.Equal(t, 9091, cfg.WSPort)
	assert.Equal(t, 600, cfg.CacheTTLSeconds)
	assert.InDelta(t, 0.1, cfg.CacheSimilarityThreshold, 1e-9)
	assert.Equal(t, 60, cfg.WSPingIntervalSeconds)
	assert.Equal(t, 20, cfg.WSPongTimeoutSeconds)
	assert.Equal(t, 512, cfg.WSSendBufferSize)
	assert.Equal(t, "debug", cfg.LogLevel)
}
