package common

import (
	"fmt"
	"regexp"
	"strings"
)

var videoResolutionKeyPattern = regexp.MustCompile(`^(?:[1-9][0-9]{2,4}p|[1-9][0-9]*k)$`)

// NormalizeVideoResolutionKey returns the canonical key used by video pricing.
func NormalizeVideoResolutionKey(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if !videoResolutionKeyPattern.MatchString(normalized) {
		return "", fmt.Errorf("invalid canonical video resolution: %s", value)
	}
	return normalized, nil
}
