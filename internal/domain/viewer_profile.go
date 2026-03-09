package domain

import "time"

// ViewerProfile represents a fan's experience profile stored in Redis.
type ViewerProfile struct {
	ViewerID          string    `json:"viewer_id"`
	DisplayName       string    `json:"display_name"`
	Level             int       `json:"level"`
	ExpertiseTags     []string  `json:"expertise_tags"`
	ContributionScore int       `json:"contribution_score"`
	LastActive        time.Time `json:"last_active"`
}

// DefaultProfile returns a new profile with zero contribution and empty tags.
func DefaultProfile(viewerID, displayName string) *ViewerProfile {
	return &ViewerProfile{
		ViewerID:          viewerID,
		DisplayName:       displayName,
		Level:             0,
		ExpertiseTags:     []string{},
		ContributionScore: 0,
		LastActive:        time.Now(),
	}
}
