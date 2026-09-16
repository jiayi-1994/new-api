package helper

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// BuildVideoResolutionPriceData constructs the direct resolution price data
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
	if info.TaskRelayInfo == nil || info.TaskRelayInfo.BillingPlan == nil || info.TaskRelayInfo.BillingPlan.Kind() != relaycommon.TaskBillingKindVideoResolution {
		return hosttypes.PriceData{}, nil, fmt.Errorf("video billing requires a frozen resolution plan")
	}

	resolved, err := relaycommon.NewResolvedVideoBilling(selection, selectedResolutionPrice)
	if err != nil {
		return hosttypes.PriceData{}, nil, err
	}
	quotaPerUnit := common.QuotaPerUnit
	resolved.QuotaPerUnit = quotaPerUnit
	resolved.BillingUnit = info.TaskRelayInfo.BillingPlan.BillingUnit()
	groupRatioInfo := HandleGroupRatio(c, info)
	quota, clamp, err := relaycommon.CalculateVideoResolutionQuotaAtUnit(
		resolved.SelectedResolutionPrice,
		resolved.Selection.EffectiveDurationSeconds,
		groupRatioInfo.GroupRatio,
		resolved.Selection.IndependentRatios,
		quotaPerUnit,
		resolved.Selection.InputVideoSeconds,
		resolved.Selection.InputVideoPricePerSecond,
		resolved.BillingUnit,
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

	info.ResolvedVideoBilling = resolved
	return priceData, clamp, nil
}
