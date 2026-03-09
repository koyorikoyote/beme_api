package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/beme/beme/internal/adapter/chat"
	httphandler "github.com/beme/beme/internal/adapter/http"
	redisadapter "github.com/beme/beme/internal/adapter/redis"
	"github.com/beme/beme/internal/adapter/vllm"
	"github.com/beme/beme/internal/adapter/ws"
	"github.com/beme/beme/internal/domain"
	"github.com/beme/beme/internal/usecase"
	"github.com/beme/beme/pkg/config"
	"github.com/beme/beme/pkg/logger"
	_ "github.com/beme/beme/pkg/metrics" // register metrics as side effect
	"github.com/beme/beme/pkg/tracing"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "beme: fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// 1. Load config.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// 2. Init logger.
	log, err := logger.New(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	defer log.Sync() //nolint:errcheck

	// 3. Init tracer (defer shutdown).
	tracerShutdown, err := tracing.InitTracer("beme")
	if err != nil {
		log.Warn("tracing init failed, continuing without tracing", zap.Error(err))
	} else {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := tracerShutdown(shutdownCtx); err != nil {
				log.Warn("tracer shutdown error", zap.Error(err))
			}
		}()
	}

	// 4. Create Redis client.
	redisClient := goredis.NewClient(&goredis.Options{
		Addr:     cfg.RedisURL,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	defer redisClient.Close()

	// 5. Create repositories.
	profileRepo := redisadapter.NewRedisProfileRepo(redisClient)
	semanticCacheRepo := redisadapter.NewRedisSemanticCacheRepo(redisClient)
	rateLimiterRepo := redisadapter.NewRedisRateLimiterRepo(redisClient, cfg.RateLimitRPS)

	// 6. Ensure indexes.
	bgCtx := context.Background()
	if err := profileRepo.EnsureIndex(bgCtx); err != nil {
		log.Warn("profile repo index creation failed", zap.Error(err))
	}
	if err := semanticCacheRepo.EnsureIndex(bgCtx); err != nil {
		log.Warn("semantic cache repo index creation failed", zap.Error(err))
	}

	// 7. Create vLLM client.
	vllmTimeout := time.Duration(cfg.VLLMTimeoutSeconds) * time.Second
	vllmClient := vllm.NewVLLMClient(
		cfg.VLLMEndpoint+"/v1/chat/completions",
		cfg.VLLMModelName,
		vllmTimeout,
		log,
	)

	// 8. Create embedding client.
	embeddingClient := vllm.NewEmbeddingClient(
		cfg.VLLMEndpoint+"/v1/embeddings",
		cfg.EmbeddingModelName,
		vllmTimeout,
		log,
	)

	// 9. Create chat provider and connect.
	chatProvider := chat.NewRESTChatProvider(1000)
	if err := chatProvider.Connect(bgCtx); err != nil {
		return fmt.Errorf("chat provider connect: %w", err)
	}
	defer chatProvider.Disconnect(bgCtx) //nolint:errcheck

	// 10. Get message channel from chat provider.
	providerCh, err := chatProvider.Receive(bgCtx)
	if err != nil {
		return fmt.Errorf("chat provider receive: %w", err)
	}

	// 11. Create WebSocket hub.
	hub := ws.NewWebSocketHub(
		30*time.Second,
		10*time.Second,
		cfg.WSSendBufferSize,
		log,
	)

	// 12. Create use cases.
	semanticCache := usecase.NewSemanticCacheUseCase(
		semanticCacheRepo,
		embeddingClient,
		cfg.CacheSimilarityThreshold,
		time.Duration(cfg.CacheTTLSeconds)*time.Second,
		log,
	)
	rateLimiterUC := usecase.NewRateLimiterUseCase(rateLimiterRepo, log)

	msgChan := make(chan domain.ChatMessage, 1000)
	experienceMatcher := usecase.NewExperienceMatcherUseCase(profileRepo, cfg.PriorityThreshold, msgChan)
	batcher := usecase.NewBionicBatcherUseCase(
		msgChan,
		vllmClient,
		semanticCache,
		hub,
		time.Duration(cfg.BatchWindowSeconds)*time.Second,
		log,
	)

	// 13. Create HTTP handlers.
	handlers := httphandler.NewHandlers(rateLimiterUC, chatProvider, redisClient, cfg.VLLMEndpoint, log)

	// 14. Set up HTTP mux and register routes.
	mux := http.NewServeMux()
	handlers.RegisterRoutes(mux)
	mux.HandleFunc("/ws/hud", hub.ServeWS)
	mux.Handle("/simulator/", http.StripPrefix("/simulator/", http.FileServer(http.Dir("web/simulator"))))

	// Root context with cancellation for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 15. Start background goroutines.
	go hub.Run(ctx)
	go batcher.Run(ctx)

	// Goroutine: read from chat provider channel and process each message.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-providerCh:
				if !ok {
					return
				}
				if err := experienceMatcher.ProcessMessage(ctx, msg); err != nil {
					log.Warn("experience matcher: process message error", zap.Error(err))
				}
			}
		}
	}()

	// 16. Start HTTP server.
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.HTTPPort),
		Handler: mux,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("HTTP server starting", zap.Int("port", cfg.HTTPPort))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	// 17. Wait for SIGTERM/SIGINT or server error.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-quit:
		log.Info("received shutdown signal", zap.String("signal", sig.String()))
	case err := <-serverErr:
		log.Error("HTTP server error", zap.Error(err))
		return err
	}

	// Graceful shutdown: cancel context, drain in-flight work, shut down server.
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	log.Info("shutting down HTTP server")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("HTTP server shutdown error", zap.Error(err))
	}

	log.Info("shutdown complete")
	return nil
}
