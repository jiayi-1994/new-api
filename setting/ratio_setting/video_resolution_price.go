package ratio_setting

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

const VideoResolutionPriceOptionKey = "VideoResolutionPrice"

var (
	videoResolutionPriceMu sync.RWMutex
	videoResolutionPrices  = make(map[string]map[string]float64)
)

func parseVideoResolutionPriceJSON(value string) (map[string]map[string]float64, error) {
	rawValue := json.RawMessage(strings.TrimSpace(value))
	if err := common.ValidateJSONNoDuplicateKeys(rawValue); err != nil {
		return nil, fmt.Errorf("parse video resolution prices: %w", err)
	}
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

func matchingVideoResolutionPricesLocked(model string) (map[string]float64, bool) {
	matchName := FormatMatchingModelName(model)
	prices, ok := videoResolutionPrices[matchName]
	if !ok && strings.HasSuffix(matchName, CompactModelSuffix) {
		prices, ok = videoResolutionPrices[CompactWildcardModelKey]
	}
	return prices, ok
}

func ValidateVideoResolutionPriceByJSONString(value string) error {
	_, err := parseVideoResolutionPriceJSON(value)
	return err
}

func UpdateVideoResolutionPriceByJSONString(value string) error {
	prices, err := parseVideoResolutionPriceJSON(value)
	if err != nil {
		return err
	}
	videoResolutionPriceMu.Lock()
	videoResolutionPrices = prices
	videoResolutionPriceMu.Unlock()
	InvalidateExposedDataCache()
	return nil
}

func VideoResolutionPrice2JSONString() string {
	videoResolutionPriceMu.RLock()
	prices := cloneVideoResolutionPriceMap(videoResolutionPrices)
	videoResolutionPriceMu.RUnlock()
	data, err := common.Marshal(prices)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func GetVideoResolutionPriceMap() map[string]map[string]float64 {
	videoResolutionPriceMu.RLock()
	defer videoResolutionPriceMu.RUnlock()
	return cloneVideoResolutionPriceMap(videoResolutionPrices)
}

func GetVideoResolutionPrices(model string) (map[string]float64, bool) {
	videoResolutionPriceMu.RLock()
	defer videoResolutionPriceMu.RUnlock()
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
	videoResolutionPriceMu.RLock()
	defer videoResolutionPriceMu.RUnlock()
	_, ok := matchingVideoResolutionPricesLocked(model)
	return ok
}

func GetVideoResolutionPrice(model, resolution string) (float64, bool) {
	normalized, err := common.NormalizeVideoResolutionKey(resolution)
	if err != nil {
		return 0, false
	}
	videoResolutionPriceMu.RLock()
	defer videoResolutionPriceMu.RUnlock()
	prices, ok := matchingVideoResolutionPricesLocked(model)
	if !ok {
		return 0, false
	}
	price, ok := prices[normalized]
	return price, ok
}
