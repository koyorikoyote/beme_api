package chat

import (
	"context"
	"fmt"
	"time"

	"github.com/beme/beme/internal/domain"
)

// IncomingMessage is the payload received from the HTTP handler via POST /api/chat.
type IncomingMessage struct {
	ViewerID    string `json:"viewer_id"`
	DisplayName string `json:"display_name"`
	Content     string `json:"content"`
}

// RESTChatProvider implements usecase.ChatProvider by accepting messages
// submitted by the HTTP handler through an internal channel.
type RESTChatProvider struct {
	bufferSize int
	ch         chan domain.ChatMessage
}

// NewRESTChatProvider creates a new RESTChatProvider with the given channel buffer size.
func NewRESTChatProvider(bufferSize int) *RESTChatProvider {
	return &RESTChatProvider{bufferSize: bufferSize}
}

// Connect initializes the internal message channel.
func (p *RESTChatProvider) Connect(_ context.Context) error {
	p.ch = make(chan domain.ChatMessage, p.bufferSize)
	return nil
}

// Receive returns the read-only message channel for consumers.
func (p *RESTChatProvider) Receive(_ context.Context) (<-chan domain.ChatMessage, error) {
	return p.ch, nil
}

// Disconnect closes the internal message channel.
func (p *RESTChatProvider) Disconnect(_ context.Context) error {
	close(p.ch)
	return nil
}

// Submit converts an IncomingMessage to a domain.ChatMessage and sends it
// to the internal channel in a non-blocking fashion. Messages are dropped
// if the channel buffer is full.
func (p *RESTChatProvider) Submit(msg IncomingMessage) error {
	chatMsg := domain.ChatMessage{
		MessageID:   fmt.Sprintf("%d", time.Now().UnixNano()),
		ViewerID:    msg.ViewerID,
		DisplayName: msg.DisplayName,
		Content:     msg.Content,
		Priority:    domain.PriorityStandard,
		Timestamp:   time.Now(),
	}
	select {
	case p.ch <- chatMsg:
	default:
	}
	return nil
}
