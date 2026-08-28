package gemini

import (
	"fmt"
	"strconv"
	"strings"

	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

type VeoProvider string

const (
	VeoProviderGemini VeoProvider = "gemini"
	VeoProviderVertex VeoProvider = "vertex"
)

type veoVideoCapability struct {
	resolutions                   map[string]bool
	durations                     map[int]bool
	portraitUnsupportedResolution map[string]bool
	highResEight                  bool
}

var veoVideoCapabilities = map[VeoProvider]map[string]veoVideoCapability{
	VeoProviderGemini: {
		"veo-3.1-generate-preview": {
			resolutions: map[string]bool{"720p": true, "1080p": true, "4k": true},
			durations:   map[int]bool{4: true, 6: true, 8: true}, highResEight: true,
		},
		"veo-3.1-fast-generate-preview": {
			resolutions: map[string]bool{"720p": true, "1080p": true, "4k": true},
			durations:   map[int]bool{4: true, 6: true, 8: true}, highResEight: true,
		},
	},
	VeoProviderVertex: {
		"veo-3.1-generate-001": {
			resolutions: map[string]bool{"720p": true, "1080p": true},
			durations:   map[int]bool{4: true, 6: true, 8: true},
		},
		"veo-3.1-fast-generate-001": {
			resolutions: map[string]bool{"720p": true, "1080p": true},
			durations:   map[int]bool{4: true, 6: true, 8: true},
		},
		"veo-3.0-generate-001": {
			resolutions: map[string]bool{"720p": true, "1080p": true},
			durations:   map[int]bool{4: true, 6: true, 8: true},
		},
		"veo-3.0-fast-generate-001": {
			resolutions: map[string]bool{"720p": true, "1080p": true},
			durations:   map[int]bool{4: true, 6: true, 8: true},
		},
		"veo-3.1-generate-preview": {
			resolutions: map[string]bool{"720p": true, "1080p": true},
			durations:   map[int]bool{4: true, 6: true, 8: true},
		},
		"veo-3.1-fast-generate-preview": {
			resolutions: map[string]bool{"720p": true, "1080p": true},
			durations:   map[int]bool{4: true, 6: true, 8: true},
		},
	},
}

// ResolveVeoVideoRequest applies the same provider defaults and capability
// checks used by Gemini and Vertex payload builders.
func ResolveVeoVideoRequest(req relaycommon.TaskSubmitReq, upstreamModel string, provider VeoProvider) (*VeoParameters, relaycommon.VideoBillingSelection, error) {
	providerCapabilities, ok := veoVideoCapabilities[provider]
	if !ok {
		return nil, relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported Veo provider %q", provider)
	}
	capability, ok := providerCapabilities[upstreamModel]
	if !ok {
		return nil, relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported %s Veo model %q", provider, upstreamModel)
	}

	params := &VeoParameters{}
	if err := taskcommon.UnmarshalMetadata(req.Metadata, params); err != nil {
		return nil, relaycommon.VideoBillingSelection{}, err
	}

	resolution := strings.ToLower(strings.TrimSpace(params.Resolution))
	if resolution == "" && strings.TrimSpace(req.Resolution) != "" {
		resolution = strings.ToLower(strings.TrimSpace(req.Resolution))
	}
	if resolution == "" && strings.TrimSpace(req.Size) != "" {
		var mapped bool
		resolution, _, mapped = mapVeoSize(req.Size)
		if !mapped {
			return nil, relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported Veo size %q", req.Size)
		}
	}
	if resolution == "" {
		resolution = "720p"
	}
	if !capability.resolutions[resolution] {
		return nil, relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported %s Veo resolution %q", provider, resolution)
	}

	duration := params.DurationSeconds
	if duration == 0 {
		duration = req.Duration
	}
	if duration == 0 && strings.TrimSpace(req.Seconds) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(req.Seconds))
		if err != nil {
			return nil, relaycommon.VideoBillingSelection{}, fmt.Errorf("invalid Veo duration %q", req.Seconds)
		}
		duration = parsed
	}
	if duration == 0 {
		duration = 8
	}
	if !capability.durations[duration] {
		return nil, relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported %s Veo duration %d", provider, duration)
	}
	if capability.highResEight && resolution != "720p" && duration != 8 {
		return nil, relaycommon.VideoBillingSelection{}, fmt.Errorf("Veo resolution %q requires an 8-second duration", resolution)
	}

	if params.AspectRatio == "" && strings.TrimSpace(req.Size) != "" {
		_, aspectRatio, mapped := mapVeoSize(req.Size)
		if !mapped {
			return nil, relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported Veo size %q", req.Size)
		}
		params.AspectRatio = aspectRatio
	}
	if params.AspectRatio != "" && params.AspectRatio != "16:9" && params.AspectRatio != "9:16" {
		return nil, relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported %s Veo aspect ratio %q", provider, params.AspectRatio)
	}
	if capability.portraitUnsupportedResolution[resolution] && params.AspectRatio == "9:16" {
		return nil, relaycommon.VideoBillingSelection{}, fmt.Errorf("unsupported %s Veo portrait output at %s for model %s", provider, resolution, upstreamModel)
	}
	params.Resolution = resolution
	params.DurationSeconds = duration
	params.SampleCount = 1

	return params, relaycommon.VideoBillingSelection{
		EffectiveResolution:      resolution,
		EffectiveDurationSeconds: duration,
	}, nil
}

func mapVeoSize(size string) (resolution, aspectRatio string, ok bool) {
	normalized := strings.ToLower(strings.TrimSpace(size))
	normalized = strings.ReplaceAll(normalized, "*", "x")
	switch normalized {
	case "720p", "1280x720":
		return "720p", "16:9", true
	case "720x1280":
		return "720p", "9:16", true
	case "1080p", "1920x1080":
		return "1080p", "16:9", true
	case "1080x1920":
		return "1080p", "9:16", true
	case "4k", "3840x2160":
		return "4k", "16:9", true
	case "2160x3840":
		return "4k", "9:16", true
	default:
		return "", "", false
	}
}

// ParseVeoDurationSeconds extracts durationSeconds from metadata.
// Returns 8 (Veo default) when not specified or invalid.
func ParseVeoDurationSeconds(metadata map[string]any) int {
	if metadata == nil {
		return 8
	}
	v, ok := metadata["durationSeconds"]
	if !ok {
		return 8
	}
	switch n := v.(type) {
	case float64:
		if int(n) > 0 {
			return int(n)
		}
	case int:
		if n > 0 {
			return n
		}
	}
	return 8
}

// ParseVeoResolution extracts resolution from metadata.
// Returns "720p" when not specified.
func ParseVeoResolution(metadata map[string]any) string {
	if metadata == nil {
		return "720p"
	}
	v, ok := metadata["resolution"]
	if !ok {
		return "720p"
	}
	if s, ok := v.(string); ok && s != "" {
		return strings.ToLower(s)
	}
	return "720p"
}

// ResolveVeoDuration returns the effective duration in seconds.
// Priority: metadata["durationSeconds"] > stdDuration > stdSeconds > default (8).
// The result is capped because it is used as a billing multiplier and the
// metadata path bypasses standard request validation.
func ResolveVeoDuration(metadata map[string]any, stdDuration int, stdSeconds string) int {
	if metadata != nil {
		if _, exists := metadata["durationSeconds"]; exists {
			if d := ParseVeoDurationSeconds(metadata); d > 0 {
				return min(d, relaycommon.MaxTaskDurationSeconds)
			}
		}
	}
	if stdDuration > 0 {
		return min(stdDuration, relaycommon.MaxTaskDurationSeconds)
	}
	if s, err := strconv.Atoi(stdSeconds); err == nil && s > 0 {
		return min(s, relaycommon.MaxTaskDurationSeconds)
	}
	return 8
}

// ResolveVeoResolution returns the effective resolution string (lowercase).
// Priority: metadata["resolution"] > SizeToVeoResolution(stdSize) > default ("720p").
func ResolveVeoResolution(metadata map[string]any, stdSize string) string {
	if metadata != nil {
		if _, exists := metadata["resolution"]; exists {
			if r := ParseVeoResolution(metadata); r != "" {
				return r
			}
		}
	}
	if stdSize != "" {
		return SizeToVeoResolution(stdSize)
	}
	return "720p"
}

// SizeToVeoResolution converts a "WxH" size string to a Veo resolution label.
func SizeToVeoResolution(size string) string {
	parts := strings.SplitN(strings.ToLower(size), "x", 2)
	if len(parts) != 2 {
		return "720p"
	}
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	maxDim := w
	if h > maxDim {
		maxDim = h
	}
	if maxDim >= 3840 {
		return "4k"
	}
	if maxDim >= 1920 {
		return "1080p"
	}
	return "720p"
}

// SizeToVeoAspectRatio converts a "WxH" size string to a Veo aspect ratio.
func SizeToVeoAspectRatio(size string) string {
	parts := strings.SplitN(strings.ToLower(size), "x", 2)
	if len(parts) != 2 {
		return "16:9"
	}
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	if w <= 0 || h <= 0 {
		return "16:9"
	}
	if h > w {
		return "9:16"
	}
	return "16:9"
}

// VeoResolutionRatio returns the pricing multiplier for the given resolution.
// Standard resolutions (720p, 1080p) return 1.0.
// 4K returns a model-specific multiplier based on Google's official pricing.
func VeoResolutionRatio(modelName, resolution string) float64 {
	if resolution != "4k" {
		return 1.0
	}
	// 4K multipliers derived from Vertex AI official pricing (video+audio base):
	//   veo-3.1-generate:      $0.60 / $0.40 = 1.5
	//   veo-3.1-fast-generate: $0.35 / $0.15 ≈ 2.333
	// Veo 3.0 models do not support 4K; return 1.0 as fallback.
	if strings.Contains(modelName, "3.1-fast-generate") {
		return 2.333333
	}
	if strings.Contains(modelName, "3.1-generate") || strings.Contains(modelName, "3.1") {
		return 1.5
	}
	return 1.0
}
