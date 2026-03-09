package usecase

import (
	"context"
	"fmt"
	"sort"
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

// buildPrompt constructs the batch prompt sent to the LLM.
// Bionic-priority messages must already be sorted to the top of batch before calling.
func buildPrompt(batch []domain.ChatMessage) string {
	msgs := ""
	for i, msg := range batch {
		msgs += fmt.Sprintf("%d. [%s] %s\n", i+1, msg.DisplayName, msg.Content)
	}
	return fmt.Sprintf(`Analyze these chat messages and output ONLY a JSON array. Use curly braces for objects.

Example output format:
[{"tip_type":"sentiment","message":"Chat is excited about the stream","expert_handle":"system","sentiment_score":0.9,"batch_id":"batch-1"},{"tip_type":"expert_tip","message":"Try using hotkeys for faster gameplay","expert_handle":"ProGamer99","sentiment_score":0.8,"batch_id":"batch-1"}]

Rules:
- tip_type must be one of: sentiment, expert_tip, highlight
- message must be under 100 characters
- sentiment_score must be a number between 0.0 and 1.0
- batch_id must be "batch-%d"
- expert_handle is the chatter's display name, or "system" if none applies
- Output ONLY the JSON array, nothing else

Chat messages:
%s`, len(batch), msgs)
}
