package usecase

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/beme/beme/internal/domain"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

var (
	batchProcessingDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "beme_batch_processing_duration_seconds",
		Help:    "Duration of each batch dispatch in seconds.",
		Buckets: prometheus.DefBuckets,
	})
	chatMessagesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "beme_chat_messages_total",
		Help: "Total number of chat messages received by the batcher.",
	})
)

func init() {
	prometheus.MustRegister(batchProcessingDuration, chatMessagesTotal)
}

// BionicBatcherUseCase collects chat messages in a sliding time window, dispatches
// batches to the LLM (via semantic cache), and broadcasts resulting Tip Cards to
// the VTuber HUD.
type BionicBatcherUseCase struct {
	in          <-chan domain.ChatMessage
	llm         LLMClient
	cache       *SemanticCacheUseCase
	broadcaster HUDBroadcaster
	window      time.Duration
	logger      *zap.Logger
}

// NewBionicBatcherUseCase constructs a BionicBatcherUseCase.
func NewBionicBatcherUseCase(
	in <-chan domain.ChatMessage,
	llm LLMClient,
	cache *SemanticCacheUseCase,
	broadcaster HUDBroadcaster,
	window time.Duration,
	logger *zap.Logger,
) *BionicBatcherUseCase {
	return &BionicBatcherUseCase{
		in:          in,
		llm:         llm,
		cache:       cache,
		broadcaster: broadcaster,
		window:      window,
		logger:      logger,
	}
}

// Run starts the main collection loop. It collects messages into a sliding window
// buffer and dispatches batches when the window elapses. Runs until ctx is cancelled.
func (b *BionicBatcherUseCase) Run(ctx context.Context) {
	ticker := time.NewTicker(b.window)
	defer ticker.Stop()

	var buffer []domain.ChatMessage

	for {
		select {
		case <-ctx.Done():
			return

		case msg, ok := <-b.in:
			if !ok {
				return
			}
			chatMessagesTotal.Inc()
			buffer = append(buffer, msg)

		case <-ticker.C:
			if len(buffer) == 0 {
				continue
			}
			// Snapshot the current buffer and reset immediately so new messages
			// are collected into a fresh buffer while dispatch runs concurrently.
			batch := buffer
			buffer = nil

			go b.dispatch(ctx, batch)
		}
	}
}

// dispatch sorts the batch, constructs a prompt, checks the semantic cache, calls
// the LLM on a miss, stores the result, and broadcasts Tip Cards to the HUD.
func (b *BionicBatcherUseCase) dispatch(ctx context.Context, batch []domain.ChatMessage) {
	start := time.Now()
	defer func() {
		batchProcessingDuration.Observe(time.Since(start).Seconds())
	}()

	// Sort: bionic-priority messages first, then standard.
	sort.SliceStable(batch, func(i, j int) bool {
		return batch[i].Priority == domain.PriorityBionic && batch[j].Priority != domain.PriorityBionic
	})

	prompt := buildPrompt(batch)

	// Check semantic cache first.
	cards, hit, err := b.cache.Check(ctx, prompt)
	if err != nil {
		b.logger.Warn("bionic batcher: cache check error", zap.Error(err))
	}

	if hit {
		if broadcastErr := b.broadcaster.Broadcast(ctx, cards); broadcastErr != nil {
			b.logger.Warn("bionic batcher: broadcast failed (cache hit)", zap.Error(broadcastErr))
		} else {
			b.logger.Info("bionic batcher: AI Actions broadcast (cache hit)", zap.Int("card_count", len(cards)))
		}
		return
	}

	// Cache miss — call the LLM.
	cards, err = b.llm.InferBatch(ctx, prompt)
	if err != nil {
		b.logger.Error("bionic batcher: LLM inference failed, skipping batch", zap.Error(err), zap.Int("batch_size", len(batch)))
		return
	}

	// Store in cache (best-effort; errors are non-fatal).
	if storeErr := b.cache.Store(ctx, prompt, cards); storeErr != nil {
		b.logger.Warn("bionic batcher: failed to store in semantic cache", zap.Error(storeErr))
	}

	if broadcastErr := b.broadcaster.Broadcast(ctx, cards); broadcastErr != nil {
		b.logger.Warn("bionic batcher: broadcast failed (LLM result)", zap.Error(broadcastErr))
	} else {
		b.logger.Info("bionic batcher: AI Actions broadcast (LLM result)", zap.Int("card_count", len(cards)))
	}
}

// batchContentHash returns a short SHA-256 hex digest of the message IDs and
// contents in the batch. Embedding this in the prompt ensures that each unique
// set of chat messages produces a distinct vector, preventing stale cache hits
// when chat content changes between windows.
func batchContentHash(batch []domain.ChatMessage) string {
	var sb strings.Builder
	for _, msg := range batch {
		sb.WriteString(msg.MessageID)
		sb.WriteByte('|')
		sb.WriteString(msg.Content)
		sb.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return fmt.Sprintf("%x", sum[:8])
}

// buildPrompt constructs the batch prompt sent to the LLM.
// Bionic-priority messages must already be sorted to the top of batch before calling.
// A content hash is embedded so that each unique batch of messages produces a
// distinct embedding, preventing stale cache hits when chat content changes.
func buildPrompt(batch []domain.ChatMessage) string {
	var sb strings.Builder
	for i, msg := range batch {
		tag := ""
		if msg.Priority == domain.PriorityBionic {
			tag = " [⚡]"
		}
		fmt.Fprintf(&sb, "%d. [%s]%s %s\n", i+1, msg.DisplayName, tag, msg.Content)
	}
	hash := batchContentHash(batch)
	n := len(batch)
	return fmt.Sprintf(`You are analyzing a batch of live stream chat messages. Synthesize the key themes into meaningful, actionable tip cards for the streamer's HUD.

Output ONLY a JSON array of 1-3 tip cards. No markdown, no explanation.

Example:
[{"tip_type":"sentiment","message":"Chat is hyped about the boss fight — keep the energy up","expert_handle":"system","sentiment_score":0.88,"batch_id":"batch-%d"},{"tip_type":"expert_tip","message":"ProGamer99 suggests using the dodge roll here","expert_handle":"ProGamer99","sentiment_score":0.75,"batch_id":"batch-%d"}]

Rules:
- tip_type: "sentiment" for mood/energy summaries, "expert_tip" for actionable advice from a chatter, "highlight" for notable moments
- message: a synthesized, meaningful insight (not a quote) — max 100 characters
- sentiment_score: 0.0 to 1.0
- batch_id: "batch-%d"
- expert_handle: the most relevant chatter's display name, or "system"
- Prioritize messages marked [⚡] (bionic priority)
- Output ONLY the JSON array

[hash:%s] Chat messages:
%s`, n, n, n, hash, sb.String())
}
