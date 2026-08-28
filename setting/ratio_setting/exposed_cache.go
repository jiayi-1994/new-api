package ratio_setting

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

const exposedDataTTL = 30 * time.Second

type exposedCache struct {
	data       gin.H
	expiresAt  time.Time
	generation uint64
}

var (
	exposedData           atomic.Value
	exposedDataGeneration atomic.Uint64
	rebuildMu             sync.Mutex
)

func InvalidateExposedDataCache() {
	exposedDataGeneration.Add(1)
	exposedData.Store((*exposedCache)(nil))
}

var buildExposedDataSnapshot = func() map[string]any {
	return map[string]any{
		"model_ratio":            GetModelRatioCopy(),
		"completion_ratio":       GetCompletionRatioCopy(),
		"cache_ratio":            GetCacheRatioCopy(),
		"create_cache_ratio":     GetCreateCacheRatioCopy(),
		"model_price":            GetModelPriceCopy(),
		"video_resolution_price": GetVideoResolutionPriceMap(),
	}
}

func cloneGinH(src gin.H) gin.H {
	dst := make(gin.H, len(src))
	for k, v := range src {
		if k == "video_resolution_price" {
			if prices, ok := v.(map[string]map[string]float64); ok {
				dst[k] = cloneVideoResolutionPriceMap(prices)
				continue
			}
		}
		dst[k] = v
	}
	return dst
}

func GetExposedData() gin.H {
	for {
		generation := exposedDataGeneration.Load()
		if c, ok := exposedData.Load().(*exposedCache); ok && c != nil && c.generation == generation && time.Now().Before(c.expiresAt) {
			return cloneGinH(c.data)
		}

		rebuildMu.Lock()
		generation = exposedDataGeneration.Load()
		if c, ok := exposedData.Load().(*exposedCache); ok && c != nil && c.generation == generation && time.Now().Before(c.expiresAt) {
			data := cloneGinH(c.data)
			rebuildMu.Unlock()
			return data
		}

		newData := gin.H(buildExposedDataSnapshot())
		if generation != exposedDataGeneration.Load() {
			rebuildMu.Unlock()
			continue
		}
		exposedData.Store(&exposedCache{
			data:       newData,
			expiresAt:  time.Now().Add(exposedDataTTL),
			generation: generation,
		})
		if generation != exposedDataGeneration.Load() {
			rebuildMu.Unlock()
			continue
		}
		data := cloneGinH(newData)
		rebuildMu.Unlock()
		return data
	}
}
