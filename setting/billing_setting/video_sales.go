package billing_setting

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

const VideoSalesOption = "billing_setting.video_sales"

// VideoSalesTier is one resolution's public price and sellable output seconds.
// InputVideoUSDPerSecond is added per second of input video; 0 or absent sells
// input video for free. It is serialized even at 0 so clients read a number.
type VideoSalesTier struct {
	USDPerSecond           float64 `json:"usd_per_second"`
	InputVideoUSDPerSecond float64 `json:"input_video_usd_per_second"`
	Seconds                []int   `json:"seconds"`
}

// VideoSalesModel is a unified public video model. The client pays for the
// requested output seconds at the requested resolution, whichever channel or
// plugin executes it. A disabled entry stays unified so new requests are
// refused rather than priced by the executing plugin.
type VideoSalesModel struct {
	Disabled    bool                      `json:"disabled,omitempty"`
	Resolutions map[string]VideoSalesTier `json:"resolutions"`
}

// GetVideoSales finds a unified model by ASCII-folded name and returns its
// configured spelling, so every unified check agrees on one entry.
func GetVideoSales(model string) (string, VideoSalesModel, bool) {
	fold := jsplugin.ASCIIFold(model)
	for name, sales := range billingSetting.VideoSales {
		if jsplugin.ASCIIFold(name) == fold {
			return name, sales, true
		}
	}
	return "", VideoSalesModel{}, false
}

// CanonicalVideoTier normalizes a resolution to "<height>p" or "4k". 2160p and
// 4k name one tier because plugins report 2160p output as "4k".
func CanonicalVideoTier(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "4k" {
		return name, true
	}
	digits, ok := strings.CutSuffix(name, "p")
	height, err := strconv.Atoi(digits)
	if !ok || err != nil || height <= 0 || strconv.Itoa(height) != digits {
		return "", false
	}
	return VideoTierForHeight(height), true
}

// VideoTierForHeight names the tier of a frame whose short edge is height.
func VideoTierForHeight(height int) string {
	if height == 2160 {
		return "4k"
	}
	return strconv.Itoa(height) + "p"
}

// ValidVideoInputPrice reports whether an input video price per second is
// finite and non-negative. Both the saved document and every request entry
// check it, so a price edited around the option API can never become a credit.
func ValidVideoInputPrice(usd float64) bool {
	return usd >= 0 && !math.IsInf(usd, 1)
}

// ParseVideoSales validates and normalizes a complete video_sales document.
// A lookup must never match two entries with different prices.
func ParseVideoSales(value string) (map[string]VideoSalesModel, error) {
	var sales map[string]VideoSalesModel
	if err := common.UnmarshalJsonStr(value, &sales); err != nil {
		return nil, fmt.Errorf("invalid video_sales: %w", err)
	}
	folded := make(map[string]string, len(sales))
	for model, entry := range sales {
		if model == "" || model != strings.TrimSpace(model) {
			return nil, fmt.Errorf("video_sales: model name %q must be non-empty and trimmed", model)
		}
		fold := jsplugin.ASCIIFold(model)
		if other, duplicate := folded[fold]; duplicate {
			return nil, fmt.Errorf("video_sales: models %q and %q differ only in letter case", other, model)
		}
		folded[fold] = model
		if len(entry.Resolutions) == 0 {
			return nil, fmt.Errorf("video_sales: model %s needs at least one resolution", model)
		}
		resolutions := make(map[string]VideoSalesTier, len(entry.Resolutions))
		for tier, price := range entry.Resolutions {
			canonical, ok := CanonicalVideoTier(tier)
			if !ok {
				return nil, fmt.Errorf("video_sales: model %s: resolution %q must be <height>p or 4k", model, tier)
			}
			if _, duplicate := resolutions[canonical]; duplicate {
				return nil, fmt.Errorf("video_sales: model %s: duplicate resolution %q", model, canonical)
			}
			if math.IsNaN(price.USDPerSecond) || math.IsInf(price.USDPerSecond, 0) || price.USDPerSecond <= 0 {
				return nil, fmt.Errorf("video_sales: model %s %s: usd_per_second must be a positive number", model, tier)
			}
			if !ValidVideoInputPrice(price.InputVideoUSDPerSecond) {
				return nil, fmt.Errorf("video_sales: model %s %s: input_video_usd_per_second must be a finite number >= 0", model, tier)
			}
			if len(price.Seconds) == 0 {
				return nil, fmt.Errorf("video_sales: model %s %s needs at least one sellable duration", model, tier)
			}
			for _, seconds := range price.Seconds {
				if seconds < 1 || seconds > relaycommon.MaxTaskDurationSeconds {
					return nil, fmt.Errorf("video_sales: model %s %s: seconds must be within [1, %d]", model, tier, relaycommon.MaxTaskDurationSeconds)
				}
			}
			resolutions[canonical] = price
		}
		entry.Resolutions = resolutions
		sales[model] = entry
	}
	return sales, nil
}
