package billing_setting

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

const VideoSalesOption = "billing_setting.video_sales"

// VideoSalesTier is one resolution's public price and sellable output seconds.
// InputVideoUSDPerSecond is added per second of input video; 0 or absent sells
// input video for free. Under official reference billing it is instead the
// with-video price of the whole order: output plus billable input seconds.
// It is serialized even at 0 so clients read a number.
//
// Under token pricing the per-second prices are unused; the tier sells output
// tokens at USDPerMillionTokens and reference video at
// WithVideoUSDPerMillionTokens (0 = no extra charge), in the same added or
// official shape.
type VideoSalesTier struct {
	USDPerSecond                 float64 `json:"usd_per_second"`
	InputVideoUSDPerSecond       float64 `json:"input_video_usd_per_second"`
	USDPerMillionTokens          float64 `json:"usd_per_million_tokens,omitempty"`
	WithVideoUSDPerMillionTokens float64 `json:"with_video_usd_per_million_tokens,omitempty"`
	Seconds                      []int   `json:"seconds"`
}

// VideoSalesModel is a unified public video model. The client pays for the
// requested output seconds at the requested resolution, whichever channel or
// plugin executes it. A disabled entry stays unified so new requests are
// refused rather than priced by the executing plugin.
// OfficialReferenceBilling prices an order with input video the official
// Seedance way: input billed at no less than ceil(2/3) of the output seconds,
// and the whole order at the with-video price.
//
// PricingUnit "token" prices the order the official Ark way: tokens =
// W x H x fps x seconds / 1024 at the output size, with W x H looked up in
// DimensionProfile's official size table. Empty means per-second pricing and
// the Seedance 2.0 table.
type VideoSalesModel struct {
	Disabled                 bool                      `json:"disabled,omitempty"`
	OfficialReferenceBilling bool                      `json:"official_reference_billing,omitempty"`
	PricingUnit              string                    `json:"pricing_unit,omitempty"`
	DimensionProfile         string                    `json:"dimension_profile,omitempty"`
	Resolutions              map[string]VideoSalesTier `json:"resolutions"`
}

const (
	VideoPricingUnitToken   = "token"
	VideoDimensionProfile25 = "seedance-2.5"
	// ponytail: every Seedance upstream renders 24 fps; add a model-level
	// field when a 30 fps upstream is sold.
	VideoTokenFPS = 24
)

// videoTokenRatios orders the columns of videoTokenDimensions.
var videoTokenRatios = [...]string{"16:9", "4:3", "1:1", "3:4", "9:16", "21:9"}

// videoTokenDimensions is the official Seedance output size per dimension
// profile ("" = seedance-2.0), tier and ratio. The web editor and pricing
// page keep an identical copy; both test suites assert every cell.
var videoTokenDimensions = map[string]map[string][len(videoTokenRatios)][2]int{
	"": {
		"480p":  {{864, 496}, {752, 560}, {640, 640}, {560, 752}, {496, 864}, {992, 432}},
		"720p":  {{1280, 720}, {1112, 834}, {960, 960}, {834, 1112}, {720, 1280}, {1470, 630}},
		"1080p": {{1920, 1080}, {1664, 1248}, {1440, 1440}, {1248, 1664}, {1080, 1920}, {2206, 946}},
		"4k":    {{3840, 2160}, {3326, 2494}, {2880, 2880}, {2494, 3326}, {2160, 3840}, {4398, 1886}},
	},
	VideoDimensionProfile25: {
		"480p":  {{854, 480}, {752, 560}, {640, 640}, {560, 752}, {480, 854}, {992, 432}},
		"720p":  {{1280, 720}, {1112, 834}, {960, 960}, {834, 1112}, {720, 1280}, {1470, 630}},
		"1080p": {{1920, 1080}, {1664, 1248}, {1440, 1440}, {1248, 1664}, {1080, 1920}, {2206, 946}},
	},
}

// VideoTokenDimensions is the official output size of a tier at ratio. An
// empty or "adaptive" ratio takes 16:9, the tier's widest frame.
func VideoTokenDimensions(profile, tier, ratio string) (width, height int, ok bool) {
	if ratio == "" || ratio == "adaptive" {
		ratio = videoTokenRatios[0]
	}
	sizes, found := videoTokenDimensions[profile][tier]
	index := slices.Index(videoTokenRatios[:], ratio)
	if !found || index < 0 {
		return 0, 0, false
	}
	return sizes[index][0], sizes[index][1], true
}

// VideoTokenMaxPixels is the largest official frame of a tier (4:3 beats
// 16:9 at 720p); false means the profile has no official size for it.
func VideoTokenMaxPixels(profile, tier string) (int, bool) {
	sizes, found := videoTokenDimensions[profile][tier]
	pixels := 0
	for _, size := range sizes {
		pixels = max(pixels, size[0]*size[1])
	}
	return pixels, found
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

// ValidVideoTokenPrice reports whether a no-reference price per 1M tokens is
// finite and positive; like ValidVideoInputPrice it is checked on save and on
// every request.
func ValidVideoTokenPrice(usd float64) bool {
	return usd > 0 && !math.IsInf(usd, 1)
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
		switch entry.PricingUnit {
		case "", "second":
			entry.PricingUnit = ""
		case VideoPricingUnitToken:
		default:
			return nil, fmt.Errorf("video_sales: model %s: pricing_unit must be second or token", model)
		}
		switch entry.DimensionProfile {
		case "", "seedance-2.0":
			entry.DimensionProfile = ""
		case VideoDimensionProfile25:
		default:
			return nil, fmt.Errorf("video_sales: model %s: dimension_profile must be seedance-2.0 or seedance-2.5", model)
		}
		tokenPriced := entry.PricingUnit == VideoPricingUnitToken
		resolutions := make(map[string]VideoSalesTier, len(entry.Resolutions))
		for tier, price := range entry.Resolutions {
			canonical, ok := CanonicalVideoTier(tier)
			if !ok {
				return nil, fmt.Errorf("video_sales: model %s: resolution %q must be <height>p or 4k", model, tier)
			}
			if _, duplicate := resolutions[canonical]; duplicate {
				return nil, fmt.Errorf("video_sales: model %s: duplicate resolution %q", model, canonical)
			}
			if canonical == "4k" && entry.DimensionProfile == VideoDimensionProfile25 {
				return nil, fmt.Errorf("video_sales: model %s: seedance-2.5 has no 4k output", model)
			}
			// A token tier's unused per-second price may be 0, never a credit.
			if !ValidVideoInputPrice(price.USDPerSecond) || (!tokenPriced && price.USDPerSecond <= 0) {
				return nil, fmt.Errorf("video_sales: model %s %s: usd_per_second must be a positive number", model, tier)
			}
			if !ValidVideoInputPrice(price.InputVideoUSDPerSecond) {
				return nil, fmt.Errorf("video_sales: model %s %s: input_video_usd_per_second must be a finite number >= 0", model, tier)
			}
			if tokenPriced {
				if _, official := VideoTokenMaxPixels(entry.DimensionProfile, canonical); !official {
					return nil, fmt.Errorf("video_sales: model %s %s: token pricing needs an official output size, which this dimension_profile lacks", model, tier)
				}
				if !ValidVideoTokenPrice(price.USDPerMillionTokens) {
					return nil, fmt.Errorf("video_sales: model %s %s: usd_per_million_tokens must be a positive number", model, tier)
				}
				if !ValidVideoInputPrice(price.WithVideoUSDPerMillionTokens) {
					return nil, fmt.Errorf("video_sales: model %s %s: with_video_usd_per_million_tokens must be a finite number >= 0", model, tier)
				}
				if entry.OfficialReferenceBilling && price.WithVideoUSDPerMillionTokens <= 0 {
					return nil, fmt.Errorf("video_sales: model %s %s: official reference billing needs a positive with_video_usd_per_million_tokens", model, tier)
				}
			} else if entry.OfficialReferenceBilling && price.InputVideoUSDPerSecond <= 0 {
				return nil, fmt.Errorf("video_sales: model %s %s: official reference billing needs a positive input_video_usd_per_second", model, tier)
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
