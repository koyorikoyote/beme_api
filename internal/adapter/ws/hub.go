package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/beme/beme/internal/domain"
	"github.com/beme/beme/pkg/metrics"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
)

var (
	activeConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "beme_websocket_connections_active",
		Help: "Number of active WebSocket connections to the HUD.",
	})
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// conn wraps a gorilla WebSocket connection with a buffered outbound channel.
type conn struct {
	ws       *websocket.Conn
	outbound chan []byte
	once     sync.Once // ensures unregister is sent exactly once
}

// WebSocketHub manages all active HUD WebSocket connections.
type WebSocketHub struct {
	register   chan *conn
	unregister chan *conn
	broadcast  chan []byte

	pingInterval   time.Duration
	pongTimeout    time.Duration
	sendBufferSize int
	logger         *zap.Logger

	mu      sync.RWMutex
	clients map[*conn]struct{}
}

// NewWebSocketHub creates a new hub with the given configuration.
func NewWebSocketHub(pingInterval, pongTimeout time.Duration, sendBufferSize int, logger *zap.Logger) *WebSocketHub {
	return &WebSocketHub{
		register:       make(chan *conn),
		unregister:     make(chan *conn),
		broadcast:      make(chan []byte, sendBufferSize),
		pingInterval:   pingInterval,
		pongTimeout:    pongTimeout,
		sendBufferSize: sendBufferSize,
		logger:         logger,
		clients:        make(map[*conn]struct{}),
	}
}

// Run starts the hub's main event loop. It blocks until ctx is cancelled.
func (h *WebSocketHub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			h.mu.Lock()
			for c := range h.clients {
				c.ws.Close()
				delete(h.clients, c)
			}
			h.mu.Unlock()
			return

		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()
			activeConnections.Inc()

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.outbound)
				h.mu.Unlock()
				activeConnections.Dec()
			} else {
				h.mu.Unlock()
			}

		case msg := <-h.broadcast:
			h.mu.RLock()
			for c := range h.clients {
				h.sendToConn(c, msg)
			}
			h.mu.RUnlock()
		}
	}
}

// sendToConn attempts to deliver msg to c's outbound channel.
// If the channel is full, the oldest message is dropped to make room.
func (h *WebSocketHub) sendToConn(c *conn, msg []byte) {
	for {
		select {
		case c.outbound <- msg:
			return
		default:
			// Channel full — drop oldest message.
			select {
			case <-c.outbound:
			default:
			}
		}
	}
}

// Broadcast implements usecase.HUDBroadcaster.
// It serialises each TipCard into a WSEnvelope and fans out to all clients.
func (h *WebSocketHub) Broadcast(ctx context.Context, cards []domain.TipCard) error {
	for _, card := range cards {
		env := domain.WSEnvelope{
			Type:      "tip_card",
			Payload:   card,
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		data, err := json.Marshal(env)
		if err != nil {
			h.logger.Error("ws: failed to marshal envelope", zap.Error(err))
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case h.broadcast <- data:
			metrics.HUDTipCardsSentTotal.Inc()
		}
	}
	return nil
}

// ServeWS upgrades an HTTP request to a WebSocket connection and registers it.
func (h *WebSocketHub) ServeWS(w http.ResponseWriter, r *http.Request) {
	wsConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("ws: upgrade failed", zap.Error(err))
		return
	}

	c := &conn{
		ws:       wsConn,
		outbound: make(chan []byte, h.sendBufferSize),
	}

	h.register <- c

	go h.writePump(c)
	go h.readPump(c)
}

// writePump sends outbound messages and periodic pings to the client.
func (h *WebSocketHub) writePump(c *conn) {
	ticker := time.NewTicker(h.pingInterval)
	defer func() {
		ticker.Stop()
		c.once.Do(func() { h.unregister <- c })
		c.ws.Close()
	}()

	for {
		select {
		case msg, ok := <-c.outbound:
			c.ws.SetWriteDeadline(time.Now().Add(h.pongTimeout))
			if !ok {
				// Channel closed by hub.
				c.ws.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.ws.WriteMessage(websocket.TextMessage, msg); err != nil {
				h.logger.Debug("ws: write error", zap.Error(err))
				return
			}

		case <-ticker.C:
			c.ws.SetWriteDeadline(time.Now().Add(h.pongTimeout))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				h.logger.Debug("ws: ping error", zap.Error(err))
				return
			}
		}
	}
}

// readPump reads (and discards) incoming frames and handles pong deadlines.
func (h *WebSocketHub) readPump(c *conn) {
	defer func() {
		c.once.Do(func() { h.unregister <- c })
		c.ws.Close()
	}()

	c.ws.SetReadDeadline(time.Now().Add(h.pingInterval + h.pongTimeout))
	c.ws.SetPongHandler(func(string) error {
		c.ws.SetReadDeadline(time.Now().Add(h.pingInterval + h.pongTimeout))
		return nil
	})

	for {
		if _, _, err := c.ws.ReadMessage(); err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.logger.Debug("ws: unexpected close", zap.Error(err))
			}
			return
		}
	}
}
