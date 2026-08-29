package helper

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// BuildVideoResolutionPriceData constructs the direct per-second price data
// used by video task pre-consume and settlement. Legacy TaskBillingMode is
// intentionally not consulted on this pricing path.
func BuildVideoResolutionPriceData(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	selectedResolutionPrice float64,
	selection relaycommon.VideoBillingSelection,
) (hosttypes.PriceData, *common.QuotaClamp, error) {
	if c == nil || info == nil {
		return hosttypes.PriceData{}, nil, fmt.Errorf("video billing requires relay context")
	}

	resolved, err := relaycommon.NewResolvedVideoBilling(selection, selectedResolutionPrice)
	if err != nil {
		return hosttypes.PriceData{}, nil, err
	}
	quotaPerUnit := common.QuotaPerUnit
	resolved.QuotaPerUnit = quotaPerUnit
	groupRatioInfo := HandleGroupRatio(c, info)
	quota, clamp, err := relaycommon.CalculateVideoResolutionQuotaAtUnit(
		resolved.SelectedResolutionPrice,
		resolved.Selection.EffectiveDurationSeconds,
		groupRatioInfo.GroupRatio,
		resolved.Selection.IndependentRatios,
		quotaPerUnit,
	)
	if err != nil {
		return hosttypes.PriceData{}, nil, err
	}

	priceData := hosttypes.PriceData{
		ModelPrice:     resolved.SelectedResolutionPrice,
		UsePrice:       true,
		Quota:          quota,
		GroupRatioInfo: groupRatioInfo,
	}
	for name, ratio := range resolved.Selection.IndependentRatios {
		priceData.AddOtherRatio(name, ratio)
	}

	if info.TaskRelayInfo == nil {
		info.TaskRelayInfo = &relaycommon.TaskRelayInfo{}
	}
	info.ResolvedVideoBilling = resolved
	return priceData, clamp, nil
}
