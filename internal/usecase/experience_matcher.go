package usecase

import (
	"context"
	"errors"

	"github.com/beme/beme/internal/domain"
)

// ExperienceMatcherUseCase retrieves viewer profiles, applies priority flagging,
// annotates messages, and forwards them to the batcher channel.
type ExperienceMatcherUseCase struct {
	repo      ProfileRepository
	threshold int
	out       chan<- domain.ChatMessage
}

// NewExperienceMatcherUseCase constructs an ExperienceMatcherUseCase.
// repo is used for profile lookups and upserts.
// threshold is the contribution_score value above which a message receives bionic priority.
// out is the channel to which annotated messages are forwarded.
func NewExperienceMatcherUseCase(repo ProfileRepository, threshold int, out chan<- domain.ChatMessage) *ExperienceMatcherUseCase {
	return &ExperienceMatcherUseCase{
		repo:      repo,
		threshold: threshold,
		out:       out,
	}
}

// ProcessMessage retrieves (or creates) the viewer profile, annotates the message
// with priority and profile metadata, and forwards it to the batcher channel.
func (e *ExperienceMatcherUseCase) ProcessMessage(ctx context.Context, msg domain.ChatMessage) error {
	profile, err := e.repo.GetProfile(ctx, msg.ViewerID)
	if err != nil {
		if errors.Is(err, domain.ErrProfileNotFound) {
			profile = domain.DefaultProfile(msg.ViewerID, msg.DisplayName)
			if upsertErr := e.repo.UpsertProfile(ctx, profile); upsertErr != nil {
				return upsertErr
			}
		} else {
			return err
		}
	}

	if profile.ContributionScore > e.threshold {
		msg.Priority = domain.PriorityBionic
		msg.ExpertiseTags = profile.ExpertiseTags
		msg.ContributionScore = profile.ContributionScore
	} else {
		msg.Priority = domain.PriorityStandard
	}

	select {
	case e.out <- msg:
	default:
		// Channel full — drop message to avoid blocking the caller.
	}

	return nil
}
