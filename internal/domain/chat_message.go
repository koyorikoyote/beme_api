package domain

import "time"

// Priority indicates whether a chat message has been flagged for bionic priority.
type Priority string

const (
	PriorityStandard Priority = "standard"
	PriorityBionic   Priority = "bionic"
)

// ChatMessage represents an incoming fan chat message annotated by the Experience Matcher.
type ChatMessage struct {
	MessageID         string    `json:"message_id"`
	ViewerID          string    `json:"viewer_id"`
	DisplayName       string    `json:"display_name"`
	Content           string    `json:"content"`
	Priority          Priority  `json:"priority"`
	ExpertiseTags     []string  `json:"expertise_tags,omitempty"`
	ContributionScore int       `json:"contribution_score,omitempty"`
	Timestamp         time.Time `json:"timestamp"`
}
