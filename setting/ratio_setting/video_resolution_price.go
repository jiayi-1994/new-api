package ratio_setting

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

const VideoResolutionPriceOptionKey = "VideoResolutionPrice"

var (
	videoResolutionPricingMu sync.RWMutex
	videoResolutionPrices    = make(map[string]map[string]float64)
	videoTaskBillingModes    = make(map[string]string)
)

func parseVideoResolutionPriceJSON(value string) (map[string]map[string]float64, error) {
	rawValue := json.RawMessage(strings.TrimSpace(value))
	if common.GetJsonType(rawValue) != "object" {
		return nil, fmt.Errorf("video resolution prices must be a JSON object")
	}

	var rawModels map[string]json.RawMessage
	if err := common.Unmarshal(rawValue, &rawModels); err != nil {
		return nil, fmt.Errorf("parse video resolution prices: %w", err)
	}

	prices := make(map[string]map[string]float64, len(rawModels))
	for model, rawResolutions := range rawModels {
		if common.GetJsonType(rawResolutions) != "object" {
			return nil, fmt.Errorf("video resolution prices for model %q must be a JSON object", model)
		}
		var rawPrices map[string]json.RawMessage
		if err := common.Unmarshal(rawResolutions, &rawPrices); err != nil {
			return nil, fmt.Errorf("parse video resolution prices for model %q: %w", model, err)
		}

		normalizedPrices := make(map[string]float64, len(rawPrices))
		for resolution, rawPrice := range rawPrices {
			normalized, err := common.NormalizeVideoResolutionKey(resolution)
			if err != nil {
				return nil, err
			}
			if _, exists := normalizedPrices[normalized]; exists {
				return nil, fmt.Errorf("duplicate canonical video resolution %q for model %q", normalized, model)
			}
			if common.GetJsonType(rawPrice) != "number" {
				return nil, fmt.Errorf("video resolution price for model %q at %q must be a number", model, normalized)
			}
			var price float64
			if err := common.Unmarshal(rawPrice, &price); err != nil {
				return nil, fmt.Errorf("parse video resolution price for model %q at %q: %w", model, normalized, err)
			}
			if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
				return nil, fmt.Errorf("video resolution price for model %q at %q must be positive and finite", model, normalized)
			}
			normalizedPrices[normalized] = price
		}
		prices[model] = normalizedPrices
	}
	return prices, nil
}

func parseTaskBillingModeJSON(value string) (map[string]string, error) {
	rawValue := json.RawMessage(strings.TrimSpace(value))
	if common.GetJsonType(rawValue) != "object" {
		return nil, fmt.Errorf("task billing modes must be a JSON object")
	}
	var modes map[string]string
	if err := common.Unmarshal(rawValue, &modes); err != nil {
		return nil, fmt.Errorf("parse task billing modes: %w", err)
	}
	for model, mode := range modes {
		if mode != TaskBillingModePerCall && mode != TaskBillingModePerSecond {
			return nil, fmt.Errorf("invalid task billing mode %q for model %q", mode, model)
		}
	}
	return modes, nil
}

func cloneVideoResolutionPriceMap(source map[string]map[string]float64) map[string]map[string]float64 {
	clone := make(map[string]map[string]float64, len(source))
	for model, resolutions := range source {
		resolutionClone := make(map[string]float64, len(resolutions))
		for resolution, price := range resolutions {
			resolutionClone[resolution] = price
		}
		clone[model] = resolutionClone
	}
	return clone
}

func cloneTaskBillingModeMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for model, mode := range source {
		clone[model] = mode
	}
	return clone
}

func matchingVideoResolutionPricesLocked(model string) (map[string]float64, bool) {
	matchName := FormatMatchingModelName(model)
	prices, ok := videoResolutionPrices[matchName]
	if !ok && strings.HasSuffix(matchName, CompactModelSuffix) {
		prices, ok = videoResolutionPrices[CompactWildcardModelKey]
	}
	return prices, ok
}

func matchingTaskBillingModeLocked(model string) (string, bool) {
	matchName := FormatMatchingModelName(model)
	mode, ok := videoTaskBillingModes[matchName]
	if !ok && strings.HasSuffix(matchName, CompactModelSuffix) {
		mode, ok = videoTaskBillingModes[CompactWildcardModelKey]
	}
	return mode, ok
}

func ValidateVideoResolutionPriceByJSONString(value string) error {
	_, err := parseVideoResolutionPriceJSON(value)
	return err
}

func ValidateTaskBillingModeByJSONString(value string) error {
	_, err := parseTaskBillingModeJSON(value)
	return err
}

func UpdateVideoResolutionPricingSnapshotByJSONString(priceJSON, modeJSON string) error {
	prices, err := parseVideoResolutionPriceJSON(priceJSON)
	if err != nil {
		return err
	}
	modes, err := parseTaskBillingModeJSON(modeJSON)
	if err != nil {
		return err
	}

	videoResolutionPricingMu.Lock()
	videoResolutionPrices = prices
	videoTaskBillingModes = modes
	videoResolutionPricingMu.Unlock()
	InvalidateExposedDataCache()
	return nil
}

func UpdateVideoResolutionPriceByJSONString(value string) error {
	prices, err := parseVideoResolutionPriceJSON(value)
	if err != nil {
		return err
	}
	videoResolutionPricingMu.Lock()
	videoResolutionPrices = prices
	videoResolutionPricingMu.Unlock()
	InvalidateExposedDataCache()
	return nil
}

func VideoResolutionPrice2JSONString() string {
	videoResolutionPricingMu.RLock()
	prices := cloneVideoResolutionPriceMap(videoResolutionPrices)
	videoResolutionPricingMu.RUnlock()
	data, err := common.Marshal(prices)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func GetVideoResolutionPriceMap() map[string]map[string]float64 {
	videoResolutionPricingMu.RLock()
	defer videoResolutionPricingMu.RUnlock()
	return cloneVideoResolutionPriceMap(videoResolutionPrices)
}

func GetVideoResolutionPrices(model string) (map[string]float64, bool) {
	videoResolutionPricingMu.RLock()
	defer videoResolutionPricingMu.RUnlock()
	prices, ok := matchingVideoResolutionPricesLocked(model)
	if !ok {
		return nil, false
	}
	clone := make(map[string]float64, len(prices))
	for resolution, price := range prices {
		clone[resolution] = price
	}
	return clone, true
}

func HasVideoResolutionPrice(model string) bool {
	videoResolutionPricingMu.RLock()
	defer videoResolutionPricingMu.RUnlock()
	_, ok := matchingVideoResolutionPricesLocked(model)
	return ok
}

func GetVideoResolutionPrice(model, resolution string) (float64, bool) {
	normalized, err := common.NormalizeVideoResolutionKey(resolution)
	if err != nil {
		return 0, false
	}
	videoResolutionPricingMu.RLock()
	defer videoResolutionPricingMu.RUnlock()
	prices, ok := matchingVideoResolutionPricesLocked(model)
	if !ok {
		return 0, false
	}
	price, ok := prices[normalized]
	return price, ok
}

// GetVideoResolutionBillingConfig returns price and unit from one configuration snapshot.
func GetVideoResolutionBillingConfig(model, resolution string) (price float64, mode string, ok bool) {
	normalized, err := common.NormalizeVideoResolutionKey(resolution)
	if err != nil {
		return 0, "", false
	}
	videoResolutionPricingMu.RLock()
	defer videoResolutionPricingMu.RUnlock()
	prices, found := matchingVideoResolutionPricesLocked(model)
	if !found {
		return 0, "", false
	}
	price, found = prices[normalized]
	if !found {
		return 0, "", false
	}
	mode, found = matchingTaskBillingModeLocked(model)
	if found {
		return price, mode, true
	}
	if common.StringsContains(constant.TaskPricePatches, model) {
		return price, TaskBillingModePerCall, true
	}
	return price, TaskBillingModePerSecond, true
}
