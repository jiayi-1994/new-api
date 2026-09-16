package ratio_setting

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

const VideoResolutionBillingUnitOptionKey = "VideoResolutionBillingUnit"

var videoResolutionBillingUnitMap = types.NewRWMap[string, string]()

func ValidateVideoResolutionBillingUnitByJSONString(value string) error {
	if err := common.ValidateJSONNoDuplicateKeys([]byte(value)); err != nil {
		return err
	}
	if common.GetJsonType([]byte(value)) != "object" {
		return fmt.Errorf("video resolution billing units must be a JSON object")
	}
	var units map[string]string
	if err := common.UnmarshalJsonStr(value, &units); err != nil {
		return err
	}
	for model, unit := range units {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("video resolution billing unit model key must not be blank")
		}
		if unit != TaskBillingModePerSecond && unit != TaskBillingModePerCall {
			return fmt.Errorf("invalid video resolution billing unit %q for model %q", unit, model)
		}
	}
	return nil
}

func UpdateVideoResolutionBillingUnitByJSONString(value string) error {
	if err := ValidateVideoResolutionBillingUnitByJSONString(value); err != nil {
		return err
	}
	return types.LoadFromJsonStringWithCallback(videoResolutionBillingUnitMap, value, InvalidateExposedDataCache)
}

func VideoResolutionBillingUnit2JSONString() string {
	return videoResolutionBillingUnitMap.MarshalJSONString()
}

func GetVideoResolutionBillingUnit(model string) string {
	unit, ok := videoResolutionBillingUnitMap.Get(model)
	if !ok && strings.HasSuffix(model, CompactModelSuffix) {
		unit, ok = videoResolutionBillingUnitMap.Get(CompactWildcardModelKey)
	}
	if !ok {
		return TaskBillingModePerSecond
	}
	return unit
}
