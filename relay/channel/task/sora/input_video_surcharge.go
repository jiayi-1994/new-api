package sora

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// 输入参考视频按秒附加费：模型配置了 VideoInputSecondPrice 时，提交阶段探测
// 每个参考视频的时长并写入 VideoBillingSelection；任一视频解析失败则整单
// 400 拒绝，绝不提交上游。未配置附加费的模型不产生任何探测流量。

const inputVideoSurchargeCacheKey = "sora_input_video_surcharge_seconds"

// 测试注入点
var (
	probeVideoDurationSeconds      = service.ProbeVideoDurationSeconds
	videoDurationSecondsFromReader = service.VideoDurationSecondsFromReader
)

// collectReferenceVideoURLs 是参考视频 URL 的唯一收集入口，计费与 megabyai
// 上游载荷构造共用，保证"计费与载荷不分叉"。`input_reference` 官方语义是
// 图片，不在视频键组里。
func collectReferenceVideoURLs(bodyMap map[string]any) []string {
	return megabyaiReferenceURLs(bodyMap, "referenceVideos", "reference_videos", "videos", "video")
}

func inputVideoUnresolved(err error) *dto.TaskError {
	return service.TaskErrorWrapperLocal(err, "video_input_duration_unresolved", http.StatusBadRequest)
}

// maxInputReferenceVideos caps how many reference videos one request may
// carry: each one costs outbound probe requests, so an unbounded list is an
// amplification vector. Upstream providers accept at most 3; a small margin
// keeps mixed dialects working.
const maxInputReferenceVideos = 8

// isVideoFilePart mirrors the forwarding path in BuildRequestBody, which
// sniffs untyped uploads before sending them upstream: a video must not reach
// the provider unbilled just because the client omitted its content type.
func isVideoFilePart(fh *multipart.FileHeader) bool {
	contentType := strings.ToLower(fh.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "video/") {
		return true
	}
	name := strings.ToLower(fh.Filename)
	if strings.HasSuffix(name, ".mp4") || strings.HasSuffix(name, ".mov") || strings.HasSuffix(name, ".m4v") {
		return true
	}
	if contentType == "" || contentType == "application/octet-stream" {
		f, err := fh.Open()
		if err != nil {
			return false
		}
		defer f.Close()
		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		return strings.HasPrefix(http.DetectContentType(head[:n]), "video/")
	}
	return false
}

// applyInputVideoSurcharge 在 normalize 成功后为 selection 附加输入视频秒数
// 与单价快照。探测结果按请求缓存，渠道重试不重复下载。
func applyInputVideoSurcharge(c *gin.Context, info *relaycommon.RelayInfo, selection *relaycommon.VideoBillingSelection) *dto.TaskError {
	price, ok := ratio_setting.GetVideoInputSecondPrice(info.OriginModelName, selection.EffectiveResolution)
	if !ok || price <= 0 {
		return nil
	}

	if cached, exists := c.Get(inputVideoSurchargeCacheKey); exists {
		if seconds, valid := cached.(int); valid {
			if seconds > 0 {
				selection.InputVideoSeconds = seconds
				selection.InputVideoPricePerSecond = price
			}
			return nil
		}
	}

	total := 0
	videoCount := 0
	// 每收一个视频立即累计并校验：数量上限防出站探测放大，总秒数上限在
	// 循环内即断，不等全部探测完才拒。
	accumulate := func(seconds int) *dto.TaskError {
		videoCount++
		if videoCount > maxInputReferenceVideos {
			return inputVideoUnresolved(fmt.Errorf("at most %d input reference videos are allowed", maxInputReferenceVideos))
		}
		total += seconds
		if total > relaycommon.MaxInputReferenceTotalSeconds {
			return inputVideoUnresolved(fmt.Errorf("input reference videos total %d seconds, exceeding the %d second limit", total, relaycommon.MaxInputReferenceTotalSeconds))
		}
		return nil
	}

	contentType := strings.ToLower(c.GetHeader("Content-Type"))
	switch {
	case strings.HasPrefix(contentType, "application/json"):
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return inputVideoUnresolved(err)
		}
		body, err := storage.Bytes()
		if err != nil {
			return inputVideoUnresolved(err)
		}
		var bodyMap map[string]any
		if err := common.Unmarshal(body, &bodyMap); err != nil {
			return inputVideoUnresolved(err)
		}
		videoURLs := collectReferenceVideoURLs(bodyMap)
		if len(videoURLs) > maxInputReferenceVideos {
			return inputVideoUnresolved(fmt.Errorf("at most %d input reference videos are allowed", maxInputReferenceVideos))
		}
		for _, videoURL := range videoURLs {
			seconds, err := probeVideoDurationSeconds(c.Request.Context(), videoURL)
			if err != nil {
				return inputVideoUnresolved(fmt.Errorf("failed to resolve input reference video duration for %q: %w", videoURL, err))
			}
			if taskErr := accumulate(seconds); taskErr != nil {
				return taskErr
			}
		}
	case strings.Contains(contentType, "multipart/form-data"):
		formData, err := common.ParseMultipartFormReusable(c)
		if err != nil {
			return inputVideoUnresolved(err)
		}
		for _, fileHeaders := range formData.File {
			for _, fh := range fileHeaders {
				if !isVideoFilePart(fh) {
					continue
				}
				f, err := fh.Open()
				if err != nil {
					return inputVideoUnresolved(fmt.Errorf("failed to open input reference video %q: %w", fh.Filename, err))
				}
				seconds, err := videoDurationSecondsFromReader(f)
				f.Close()
				if err != nil {
					return inputVideoUnresolved(fmt.Errorf("failed to resolve input reference video duration for %q: %w", fh.Filename, err))
				}
				if taskErr := accumulate(seconds); taskErr != nil {
					return taskErr
				}
			}
		}
	}

	c.Set(inputVideoSurchargeCacheKey, total)
	if total > 0 {
		selection.InputVideoSeconds = total
		selection.InputVideoPricePerSecond = price
	}
	return nil
}
