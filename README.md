# Bionic Experience-Matching Engine (BEME)

## Overview
The Bionic Experience-Matching Engine (BEME) is a real-time Go microservice that enables a VTuber to process 1,000+ concurrent fan interactions during live streams. The system ingests chat messages from pluggable providers, matches viewers against stored experience profiles, batches messages in a sliding time window, dispatches batches to a vLLM inference engine for sentiment summarization and expert tip extraction, caches semantically similar queries, and delivers structured Tip Cards to a VTuber HUD over WebSocket.

The system is designed around Clean Architecture principles with domain/usecase/adapter layer separation, backed by Redis Stack for profile storage, vector search, rate limiting, and semantic caching, and instrumented with Prometheus metrics and OpenTelemetry traces.

### Key Design Decisions

1. **Go as the primary language** — goroutine-per-stream concurrency model maps naturally to the fan-out/fan-in chat processing pipeline. Go's `context.Context` provides clean cancellation and timeout propagation across the entire request lifecycle.

2. **Redis Stack as the unified data layer** — RedisJSON for viewer profiles, RediSearch for indexed lookups, Redis VSS for semantic cache embeddings, and Redis Lua scripts for atomic GCRA rate limiting. A single Redis deployment reduces operational complexity.

3. **vLLM with OpenAI-compatible API** — allows model swapping without code changes. Batching messages into a single prompt per window minimizes inference calls and cost.

4. **gorilla/websocket for HUD delivery** — mature, well-tested library with per-connection read/write goroutine pattern that aligns with Go concurrency idioms.

5. **Sliding window batching via Go channels** — `select` + `time.Ticker` provides non-blocking collection with clean window rotation, avoiding complex timer management.


### How to Run 
# Start Redis
docker run -d --name redis-stack -p 6379:6379 redis/redis-stack-server:latest

# Pull the models
ollama pull qwen2.5:3b
ollama pull nomic-embed-text

# Set env vars and run
$env:VLLM_ENDPOINT="http://localhost:11434"
$env:VLLM_MODEL_NAME="qwen2.5:3b"
$env:EMBEDDING_MODEL_NAME="nomic-embed-text"
$env:REDIS_URL="localhost:6379"
go build -o beme.exe ./cmd/beme; ./beme.exe

# Once the server is up, open
http://localhost:8080/simulator/