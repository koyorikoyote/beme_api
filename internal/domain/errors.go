package domain

import "errors"

var (
	ErrProfileNotFound     = errors.New("viewer profile not found")
	ErrInvalidViewerID     = errors.New("invalid viewer ID")
	ErrBatchEmpty          = errors.New("batch contains no messages")
	ErrLLMTimeout          = errors.New("LLM inference timed out")
	ErrLLMUnavailable      = errors.New("LLM engine unavailable")
	ErrLLMResponseInvalid  = errors.New("LLM response failed validation")
	ErrSanitizationDiscard = errors.New("response discarded: sanitization removed >50% content")
	ErrCacheUnavailable    = errors.New("semantic cache unavailable")
	ErrRateLimitExceeded   = errors.New("rate limit exceeded")
	ErrConfigMissing       = errors.New("required configuration missing")
	ErrWSConnectionClosed  = errors.New("websocket connection closed")
)
