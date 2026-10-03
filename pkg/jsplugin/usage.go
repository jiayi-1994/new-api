package jsplugin

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
)

// UsageContext is the credential-free request view that submit-time usage
// hooks (extractUsage, describeSpec) receive. It carries no channel
// credentials and no relay state, so it can be built before a channel is
// chosen.
type UsageContext struct {
	// Route.RequestBody is the executing plugin's own decoded body.
	Route RouteRequestContext
	// Headers holds Content-Type and Accept.
	Headers map[string]string
	// Files is the multipart file metadata (ref, field, filename, mimeType, size).
	Files         []map[string]any
	Action        string
	Model         string // client-facing model
	UpstreamModel string // after channel model_mapping
	UsagePurpose  string // facts | billing_ratios | spec; omitted when empty
	SalesSource   string // host-owned frozen sale; omitted for plugin usage pricing
}

// BuildUsageContext renders the hook argument for u. The request body is
// copied so a hook cannot mutate host-owned values.
func BuildUsageContext(u UsageContext) map[string]any {
	ctx := u.Route.JSValue()
	ctx["requestBody"] = JSONValue(u.Route.RequestBody)
	ctx["requestHeaders"] = u.Headers
	ctx["files"] = u.Files
	ctx["action"] = u.Action
	ctx["model"] = u.Model
	ctx["upstreamModel"] = u.UpstreamModel
	if u.UsagePurpose != "" {
		ctx["usagePurpose"] = u.UsagePurpose
	}
	if u.SalesSource != "" {
		ctx["salesSource"] = u.SalesSource
	}
	return ctx
}

// JSONValue returns an isolated JSON-shaped copy of value for a hook argument.
func JSONValue(value any) any {
	if normalized, ok := CloneJSONValue(value); ok {
		return normalized
	}
	data, err := common.Marshal(value)
	if err != nil {
		return value
	}
	var normalized any
	if err = common.Unmarshal(data, &normalized); err != nil {
		return value
	}
	return normalized
}

// CloneJSONValue copies already-parsed JSON and Sobek's plain exported values
// without another encode/decode pass, isolating hooks and preserving the
// codec's float64 numbers and nulls. ok is false for any other Go type.
func CloneJSONValue(value any) (any, bool) {
	return cloneJSONValue(value, 0)
}

func cloneJSONValue(value any, depth int) (any, bool) {
	if depth > 64 {
		return nil, false
	}
	switch typed := value.(type) {
	case nil, bool:
		return typed, true
	case string:
		return typed, utf8.ValidString(typed)
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case int64:
		return float64(typed), true
	case int:
		return float64(typed), true
	case map[string]any:
		if typed == nil {
			return nil, true
		}
		cloned := make(map[string]any, len(typed))
		for key, item := range typed {
			if !utf8.ValidString(key) {
				return nil, false
			}
			child, ok := cloneJSONValue(item, depth+1)
			if !ok {
				return nil, false
			}
			cloned[key] = child
		}
		return cloned, true
	case []any:
		if typed == nil {
			return nil, true
		}
		cloned := make([]any, len(typed))
		for index, item := range typed {
			child, ok := cloneJSONValue(item, depth+1)
			if !ok {
				return nil, false
			}
			cloned[index] = child
		}
		return cloned, true
	default:
		return nil, false
	}
}

// ValidateUsageFacts validates the facts an extractUsage-style hook returned
// against the usage schema selected for models, normalizing numeric facts in
// place. It returns the positive numeric facts, which legacy per-call billing
// uses as multipliers.
func (m Meta) ValidateUsageFacts(facts map[string]any, models ...string) (map[string]float64, error) {
	usageSchema, _ := m.UsageForModels(models...)
	ratios := make(map[string]float64)
	for key, value := range facts {
		if schema, declared := usageSchema[key]; declared {
			number, err := validateUsageValue(value, schema, false)
			if err != nil {
				return nil, err
			}
			if schema.Type == "number" {
				facts[key] = number
				if number > 0 {
					ratios[key] = number
				}
			}
			continue
		}
		number, numeric := UsageNumber(value, false)
		if !numeric {
			continue
		}
		limit, canonical := canonicalUsageLimit(key)
		if !canonical {
			// Undeclared numeric facts remain extensible, but still use the
			// largest canonical task multiplier ceiling so they cannot be
			// unbounded before quota calculation.
			limit = relaycommon.MaxTaskDurationSeconds
		}
		if err := validateUsageNumberLimit(number, limit); err != nil {
			return nil, err
		}
		// Keep numeric facts usable by existing expressions even when the
		// selected profile no longer declares that legacy extension field.
		facts[key] = number
		if number > 0 {
			ratios[key] = number
		}
	}
	return ratios, nil
}

// ValidateResolvedUsageRequest bounds every usage-schema or canonical
// multiplier field found anywhere in a decoded request body.
func (m Meta) ValidateResolvedUsageRequest(request any, models ...string) error {
	schema, _ := m.UsageForModels(models...)
	return validateResolvedUsageValue(JSONValue(request), schema)
}

func validateResolvedUsageValue(value any, usageSchema map[string]UsageFieldSchema) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if schema, declared := usageSchema[key]; declared {
				if _, err := validateUsageValue(item, schema, true); err != nil {
					return err
				}
			} else if limit, canonical := canonicalUsageLimit(key); canonical {
				if err := validateUsageLimit(item, limit, true); err != nil {
					return err
				}
			}
			if err := validateResolvedUsageValue(item, usageSchema); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range typed {
			if err := validateResolvedUsageValue(item, usageSchema); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateCompletionUsageFacts validates completion-time usage facts and
// returns a normalized copy.
func (m Meta) ValidateCompletionUsageFacts(facts any, models ...string) (map[string]any, error) {
	usageSchema, _ := m.UsageForModels(models...)
	if facts == nil {
		return nil, nil
	}
	values, ok := facts.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("plugin usage hook must return an object")
	}
	validated := make(map[string]any, len(values))
	for key, value := range values {
		validated[key] = value
		if schema, declared := usageSchema[key]; declared {
			number, err := validateUsageValue(value, schema, false)
			if err != nil {
				return nil, err
			}
			if schema.Type == "number" {
				validated[key] = number
			}
			continue
		}
		if limit, canonical := canonicalUsageLimit(key); canonical {
			number, numeric := UsageNumber(value, false)
			if !numeric {
				return nil, fmt.Errorf("plugin usage value must be a number")
			}
			if err := validateUsageNumberLimit(number, limit); err != nil {
				return nil, err
			}
			validated[key] = number
			continue
		}
		switch key {
		case "upstreamUnits", "completionTokens", "totalTokens":
			number, numeric := UsageNumber(value, false)
			if !numeric || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
				return nil, fmt.Errorf("plugin usage value must be a finite non-negative number")
			}
			validated[key] = float64(common.QuotaFromFloat(number))
		default:
			if number, numeric := UsageNumber(value, false); numeric {
				validated[key] = number
			}
		}
	}
	return validated, nil
}

func validateUsageValue(value any, schema UsageFieldSchema, allowNumericString bool) (float64, error) {
	if len(schema.Enum) > 0 {
		text, ok := value.(string)
		if !ok {
			return 0, fmt.Errorf("plugin usage enum must be a string")
		}
		if slices.Contains(schema.Enum, text) {
			return 0, nil
		}
		return 0, fmt.Errorf("plugin usage enum is not an allowed value")
	}
	if schema.Type == "boolean" {
		if _, ok := value.(bool); !ok {
			return 0, fmt.Errorf("plugin usage value must be a boolean")
		}
		return 0, nil
	}
	number, ok := UsageNumber(value, allowNumericString)
	if !ok {
		return 0, fmt.Errorf("plugin usage value must be a number")
	}
	if schema.Unit == "token" || schema.Unit == "credit" {
		if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
			return 0, fmt.Errorf("plugin usage value must be a finite non-negative number")
		}
		// Bound-check with QuotaFromFloatChecked (int32 saturation) but keep
		// the original fractional part so credit facts like 3.5 survive.
		if quota, clamp := common.QuotaFromFloatChecked(number); clamp != nil {
			return float64(quota), nil
		}
		return number, nil
	}
	limit := relaycommon.MaxTaskDurationSeconds
	if schema.Unit == "count" {
		limit = dto.MaxImageN
	}
	if err := validateUsageNumberLimit(number, limit); err != nil {
		return 0, err
	}
	return number, nil
}

func validateUsageLimit(value any, limit int, allowNumericString bool) error {
	number, ok := UsageNumber(value, allowNumericString)
	if !ok {
		return fmt.Errorf("plugin usage value must be a number")
	}
	return validateUsageNumberLimit(number, limit)
}

func validateUsageNumberLimit(number float64, limit int) error {
	if math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return fmt.Errorf("plugin usage value must be a finite non-negative number")
	}
	if number > float64(limit) {
		return fmt.Errorf("plugin usage value exceeds the host limit")
	}
	return nil
}

// UsageNumber reads a JSON number a hook returned; numeric strings are
// accepted only when allowNumericString is set.
func UsageNumber(value any, allowNumericString bool) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case int64:
		return float64(number), true
	case int:
		return float64(number), true
	case string:
		if !allowNumericString {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func canonicalUsageLimit(key string) (int, bool) {
	normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
	switch normalized {
	case "duration", "durationseconds", "second", "seconds":
		return relaycommon.MaxTaskDurationSeconds, true
	case "n", "count", "imagecount", "samplecount", "batchcount", "numimages":
		return dto.MaxImageN, true
	default:
		return 0, false
	}
}
