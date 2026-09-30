// Package spec validates the object a task plugin's optional describeSpec hook
// returns and turns it into a videosched.Spec. It knows no vendor, model or
// plugin: every request field is interpreted by the plugin itself.
package spec

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/pkg/videosched"
)

// Version is the highest describeSpec contract version this host prices.
const Version = 1

// MaxOutputSeconds mirrors relay/common.MaxTaskDurationSeconds; this leaf
// package cannot import the host.
const MaxOutputSeconds = 3600

// MaxReferenceCount bounds each reference kind's count. It is a host safety
// bound above every plugin's own limit (megabyai allows 128 in total).
const MaxReferenceCount = 1024

var (
	// ErrOptOut means the plugin declares the model not schedulable.
	ErrOptOut = errors.New("model opt-out")
	// ErrVersion means spec_version is newer than Version.
	ErrVersion = errors.New("spec version unsupported")
)

// knownKeys are the top-level describeSpec fields of spec_version 1.
var knownKeys = []string{"unsupported", "spec_version", "output_seconds", "seconds_kind", "resolution", "references"}

// Parse strictly validates a describeSpec result. A missing key, a key with a
// wrong type and a legal zero are distinct: only output_seconds, seconds_kind
// and resolution may be absent. Unknown top-level keys are descriptive: they
// are not priced and are returned sorted in ignored so the caller can log
// them. Anything that changes a price must come with a new spec_version.
func Parse(raw map[string]any) (s videosched.Spec, ignored []string, err error) {
	if raw == nil {
		return s, nil, errors.New("spec must be an object")
	}
	if value, ok := raw["unsupported"]; ok {
		optOut, isBool := value.(bool)
		if !isBool {
			return s, nil, errors.New("unsupported must be a boolean")
		}
		if optOut {
			return s, nil, ErrOptOut
		}
	}

	value, ok := raw["spec_version"]
	if !ok {
		return s, nil, errors.New("spec_version is required")
	}
	version, ok := integer(value)
	if !ok || version < 1 {
		return s, nil, errors.New("spec_version must be a positive integer")
	}
	if version > Version {
		return s, nil, fmt.Errorf("%w: %d", ErrVersion, version)
	}

	references, ok := raw["references"].(map[string]any)
	if !ok {
		return s, nil, errors.New("references must be an object")
	}
	for key := range references {
		if !slices.Contains(videosched.ReferenceKinds, key) {
			return s, nil, fmt.Errorf("unknown reference kind %q", key)
		}
	}
	s.References = make(map[string]int, len(videosched.ReferenceKinds))
	for _, kind := range videosched.ReferenceKinds {
		value, ok := references[kind]
		if !ok {
			return s, nil, fmt.Errorf("references.%s is required", kind)
		}
		n, ok := integer(value)
		if !ok || n < 0 || n > MaxReferenceCount {
			return s, nil, fmt.Errorf("references.%s must be an integer between 0 and %d", kind, MaxReferenceCount)
		}
		s.References[kind] = int(n)
	}

	if value, ok := raw["output_seconds"]; ok {
		seconds, isNumber := number(value)
		if !isNumber || !(seconds > 0) || seconds > MaxOutputSeconds || math.IsInf(seconds, 0) {
			return s, nil, fmt.Errorf("output_seconds must be a number in (0, %d]", MaxOutputSeconds)
		}
		s.OutputSeconds = &seconds
	} else {
		s.Missing = append(s.Missing, "output_seconds")
	}

	s.SecondsKind = videosched.KindExact
	if value, ok := raw["seconds_kind"]; ok {
		kind, _ := value.(string)
		if kind != videosched.KindExact && kind != videosched.KindFixed {
			return s, nil, errors.New("seconds_kind must be exact or fixed")
		}
		if kind == videosched.KindFixed && s.OutputSeconds == nil {
			return s, nil, errors.New("fixed seconds_kind requires output_seconds")
		}
		s.SecondsKind = kind
	}

	if value, ok := raw["resolution"]; ok {
		tier, _ := value.(string)
		tier = strings.ToLower(strings.TrimSpace(tier))
		if tier == "" {
			return s, nil, errors.New("resolution must be a non-empty string")
		}
		if pixels, isPixels := shortSideTier(tier); isPixels {
			tier = pixels
		}
		s.Tier = tier
	} else {
		s.Missing = append(s.Missing, "resolution")
	}
	for key := range raw {
		if !slices.Contains(knownKeys, key) {
			ignored = append(ignored, key)
		}
	}
	slices.Sort(ignored)
	return s, ignored, nil
}

// number accepts only JavaScript numbers as exported by the plugin runtime
// (int64 or float64); strings, booleans and null are type errors.
func number(value any) (float64, bool) {
	switch v := value.(type) {
	case int64:
		return float64(v), true
	case int:
		return float64(v), true
	case float64:
		return v, !math.IsNaN(v)
	}
	return 0, false
}

// integer accepts a number with no fractional part.
func integer(value any) (int64, bool) {
	switch v := value.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case float64:
		if v != math.Trunc(v) || math.Abs(v) > 1<<53 {
			return 0, false
		}
		return int64(v), true
	}
	return 0, false
}
