package sanitize

import (
	"regexp"
	"strings"

	"github.com/beme/beme/internal/domain"
)

var (
	// scriptTagRe matches <script ...>...</script> including content between tags.
	scriptTagRe = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)

	// htmlTagRe matches any remaining HTML tags after script removal.
	htmlTagRe = regexp.MustCompile(`<[^>]+>`)

	// injectionPatterns are known prompt-injection phrases (lowercased for matching).
	injectionPatterns = []string{
		"ignore previous instructions",
		"system:",
		"assistant:",
	}
)

// Sanitize strips HTML tags, script elements, and prompt-injection patterns from input.
// The function is idempotent: Sanitize(Sanitize(x)) == Sanitize(x).
func Sanitize(input string) string {
	// 1. Remove <script>...</script> blocks (including content).
	result := scriptTagRe.ReplaceAllString(input, "")

	// 2. Strip remaining HTML tags.
	result = htmlTagRe.ReplaceAllString(result, "")

	// 3. Remove prompt-injection patterns (case-insensitive).
	lower := strings.ToLower(result)
	for _, pattern := range injectionPatterns {
		for {
			idx := strings.Index(lower, pattern)
			if idx == -1 {
				break
			}
			result = result[:idx] + result[idx+len(pattern):]
			lower = lower[:idx] + lower[idx+len(pattern):]
		}
	}

	return result
}

// SanitizeWithThreshold sanitizes input and returns ErrSanitizationDiscard if
// more than (1-threshold) of the original content was removed.
// For the default threshold of 0.5, it discards when >50% is removed.
func SanitizeWithThreshold(input string, threshold float64) (string, error) {
	sanitized := Sanitize(input)

	if len(input) > 0 && float64(len(sanitized)) < float64(len(input))*(1-threshold) {
		return sanitized, domain.ErrSanitizationDiscard
	}

	return sanitized, nil
}
