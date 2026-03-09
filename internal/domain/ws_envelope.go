package domain

// WSEnvelope is the typed message wrapper sent over WebSocket to HUD clients.
// Type is always "tip_card"; Timestamp is an ISO 8601 string.
type WSEnvelope struct {
	Type      string      `json:"type"`
	Payload   interface{} `json:"payload"`
	Timestamp string      `json:"timestamp"`
}
