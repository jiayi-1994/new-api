package ratio_setting

import (
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

const VideoInputSecondPriceOptionKey = "VideoInputSecondPrice"

var (
	videoInputSecondPriceMu sync.RWMutex
	videoInputSecondPrices  = make(map[string]map[string]float64)
)

// VideoInputSecondPrice 的 JSON 形状与 VideoResolutionPrice 完全一致：
// {"model": {"720p": 0.3, "1080p": 1.05}}，值是该输出分辨率下每一秒输入参考
// 视频的附加费。解析与校验直接复用分辨率价格解析器（canonical 分辨率键、
// 正有限数、无重复键）。

func ValidateVideoInputSecondPriceByJSONString(value string) error {
	_, err := parseVideoResolutionPriceJSON(value)
	return err
}

func UpdateVideoInputSecondPriceByJSONString(value string) error {
	prices, err := parseVideoResolutionPriceJSON(value)
	if err != nil {
		return err
	}
	videoInputSecondPriceMu.Lock()
	videoInputSecondPrices = prices
	videoInputSecondPriceMu.Unlock()
	return nil
}

func VideoInputSecondPrice2JSONString() string {
	videoInputSecondPriceMu.RLock()
	prices := cloneVideoResolutionPriceMap(videoInputSecondPrices)
	videoInputSecondPriceMu.RUnlock()
	data, err := common.Marshal(prices)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// GetVideoInputSecondPrice 返回模型在给定输出分辨率下的输入视频每秒附加费。
// 模型名走与分辨率价格相同的 compact 通配回退；该分辨率未配置视为无附加费。
func GetVideoInputSecondPrice(model, resolution string) (float64, bool) {
	normalized, err := common.NormalizeVideoResolutionKey(resolution)
	if err != nil {
		return 0, false
	}
	videoInputSecondPriceMu.RLock()
	defer videoInputSecondPriceMu.RUnlock()
	prices, ok := videoInputSecondPrices[model]
	if !ok && strings.HasSuffix(model, CompactModelSuffix) {
		prices, ok = videoInputSecondPrices[CompactWildcardModelKey]
	}
	if !ok {
		return 0, false
	}
	price, ok := prices[normalized]
	return price, ok
}
