package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// VLLMTTFTSeconds tracks the time-to-first-token latency for vLLM streaming responses.
var VLLMTTFTSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
	Name:    "beme_vllm_ttft_seconds",
	Help:    "Time to first token from the vLLM engine in seconds.",
	Buckets: prometheus.DefBuckets,
})

// VLLMTokensPerSecond tracks the token throughput of vLLM inference responses.
var VLLMTokensPerSecond = promauto.NewGauge(prometheus.GaugeOpts{
	Name: "beme_vllm_tokens_per_second",
	Help: "Tokens per second throughput from the vLLM engine.",
})

// RedisOperationDurationSeconds tracks the latency of Redis operations by operation type.
var RedisOperationDurationSeconds = promauto.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "beme_redis_operation_duration_seconds",
		Help:    "Duration of Redis operations in seconds.",
		Buckets: prometheus.DefBuckets,
	},
	[]string{"operation"},
)

// HUDTipCardsSentTotal counts the total number of Tip Cards sent to HUD clients.
var HUDTipCardsSentTotal = promauto.NewCounter(prometheus.CounterOpts{
	Name: "beme_hud_tip_cards_sent_total",
	Help: "Total number of Tip Cards sent to VTuber HUD WebSocket clients.",
})
