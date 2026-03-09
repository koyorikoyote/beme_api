package domain

// TipCard is a structured payload delivered to the VTuber HUD containing
// a summarized expert tip and sentiment score from a batch of chat messages.
type TipCard struct {
	TipType        string  `json:"tip_type"`
	Message        string  `json:"message"`
	ExpertHandle   string  `json:"expert_handle"`
	SentimentScore float64 `json:"sentiment_score"`
	BatchID        string  `json:"batch_id"`
}
