package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	// Redis
	RedisURL      string
	RedisPassword string
	RedisDB       int

	// LLM (vLLM or Ollama)
	VLLMEndpoint       string
	VLLMModelName      string
	VLLMTimeoutSeconds int
	EmbeddingModelName string

	// Experience matching
	PriorityThreshold int

	// Batching
	BatchWindowSeconds int

	// Rate limiting
	RateLimitRPS int

	// HTTP / WebSocket ports
	HTTPPort int
	WSPort   int

	// Semantic cache
	CacheTTLSeconds          int
	CacheSimilarityThreshold float64

	// WebSocket tuning
	WSPingIntervalSeconds int
	WSPongTimeoutSeconds  int
	WSSendBufferSize      int

	// Logging
	LogLevel string
}

// Load reads configuration from environment variables, applies defaults for
// optional parameters, and returns an error if any required parameter is absent.
func Load() (*Config, error) {
	cfg := &Config{}

	// --- Required ---
	cfg.RedisURL = os.Getenv("REDIS_URL")
	if cfg.RedisURL == "" {
		return nil, fmt.Errorf("required configuration missing: REDIS_URL")
	}

	cfg.VLLMEndpoint = os.Getenv("VLLM_ENDPOINT")
	if cfg.VLLMEndpoint == "" {
		return nil, fmt.Errorf("required configuration missing: VLLM_ENDPOINT")
	}

	// --- Optional with defaults ---
	cfg.RedisPassword = os.Getenv("REDIS_PASSWORD") // default ""
	cfg.RedisDB = getEnvInt("REDIS_DB", 0)

	cfg.VLLMModelName = getEnvString("VLLM_MODEL_NAME", "qwen2.5:3b")
	cfg.VLLMTimeoutSeconds = getEnvInt("VLLM_TIMEOUT_SECONDS", 60)
	cfg.EmbeddingModelName = getEnvString("EMBEDDING_MODEL_NAME", "nomic-embed-text")

	cfg.PriorityThreshold = getEnvInt("PRIORITY_THRESHOLD", 75)
	cfg.BatchWindowSeconds = getEnvInt("BATCH_WINDOW_SECONDS", 2)
	cfg.RateLimitRPS = getEnvInt("RATE_LIMIT_RPS", 100)

	cfg.HTTPPort = getEnvInt("HTTP_PORT", 8080)
	cfg.WSPort = getEnvInt("WS_PORT", 8081)

	cfg.CacheTTLSeconds = getEnvInt("CACHE_TTL_SECONDS", 300)
	cfg.CacheSimilarityThreshold = getEnvFloat("CACHE_SIMILARITY_THRESHOLD", 0.05)

	cfg.WSPingIntervalSeconds = getEnvInt("WS_PING_INTERVAL_SECONDS", 30)
	cfg.WSPongTimeoutSeconds = getEnvInt("WS_PONG_TIMEOUT_SECONDS", 10)
	cfg.WSSendBufferSize = getEnvInt("WS_SEND_BUFFER_SIZE", 256)

	cfg.LogLevel = getEnvString("LOG_LEVEL", "info")

	return cfg, nil
}

// getEnvString returns the env var value or the provided default.
func getEnvString(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// getEnvInt returns the env var parsed as int, or the provided default on
// absence or parse failure.
func getEnvInt(key string, defaultVal int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultVal
	}
	return n
}

// getEnvFloat returns the env var parsed as float64, or the provided default.
func getEnvFloat(key string, defaultVal float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return defaultVal
	}
	return f
}
