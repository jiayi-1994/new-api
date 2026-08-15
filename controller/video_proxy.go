package controller

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

var videoProxyResponseHeaders = [...]string{
	"Content-Length",
	"Accept-Ranges",
	"Content-Range",
	"ETag",
	"Last-Modified",
}

// videoContentTypeAliases 把厂商方言 MIME 归一化为标准值:
// S3/MinIO 系存储对未知对象默认吐 binary/octet-stream 等泛二进制类型,
// application/mp4 是 RFC 4337 注册的 mp4 合法写法。
var videoContentTypeAliases = map[string]string{
	"binary/octet-stream":        "application/octet-stream",
	"application/binary":         "application/octet-stream",
	"application/x-octet-stream": "application/octet-stream",
	"application/download":       "application/octet-stream",
	"application/x-download":     "application/octet-stream",
	"application/force-download": "application/octet-stream",
	"application/mp4":            "video/mp4",
}

var videoExtensionContentTypes = map[string]string{
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".webm": "video/webm",
	".mov":  "video/quicktime",
	".mkv":  "video/x-matroska",
}

// videoContentTypeFromURL 仅在上游只给出泛二进制类型时使用:
// 按直链路径后缀推断真实视频类型,推断不出保持 octet-stream。
func videoContentTypeFromURL(videoURL string) string {
	parsed, err := url.Parse(videoURL)
	if err != nil {
		return ""
	}
	dot := strings.LastIndex(parsed.Path, ".")
	if dot < 0 {
		return ""
	}
	return videoExtensionContentTypes[strings.ToLower(parsed.Path[dot:])]
}

func copyVideoProxyHeaders(destination, source http.Header, videoURL string) error {
	contentTypes := source.Values("Content-Type")
	if len(contentTypes) > 1 {
		return fmt.Errorf("multiple content types")
	}
	contentType := "application/octet-stream"
	if len(contentTypes) == 1 && strings.TrimSpace(contentTypes[0]) != "" {
		mediaType, _, err := mime.ParseMediaType(contentTypes[0])
		if err != nil {
			return fmt.Errorf("invalid content type")
		}
		mediaType = strings.ToLower(mediaType)
		if canonical, ok := videoContentTypeAliases[mediaType]; ok {
			mediaType = canonical
		}
		if !strings.HasPrefix(mediaType, "video/") && mediaType != "application/octet-stream" {
			return fmt.Errorf("unsupported content type")
		}
		contentType = mediaType
	}
	if contentType == "application/octet-stream" {
		if inferred := videoContentTypeFromURL(videoURL); inferred != "" {
			contentType = inferred
		}
	}

	for _, key := range videoProxyResponseHeaders {
		values := source.Values(key)
		if len(values) == 0 {
			continue
		}
		destination.Del(key)
		for _, value := range values {
			destination.Add(key, value)
		}
	}
	destination.Set("Content-Type", contentType)
	destination.Set("Content-Disposition", `attachment; filename="`+videoContentFilename(contentType)+`"`)
	destination.Set("X-Content-Type-Options", "nosniff")
	return nil
}

func videoContentFilename(contentType string) string {
	switch contentType {
	case "video/mp4":
		return "video.mp4"
	case "video/webm":
		return "video.webm"
	case "video/quicktime":
		return "video.mov"
	case "video/x-matroska":
		return "video.mkv"
	default:
		return "video.bin"
	}
}

// objectStorageHostSuffixes 列出主流对象存储/配套 CDN 的托管域名。
// 命中即认为是带时效签名的公开直链，302 让客户端直连厂商边缘节点，
// 绕开网关中转的带宽瓶颈；未命中的第三方站点仍由网关代理兜一层。
var objectStorageHostSuffixes = [...]string{
	".r2.cloudflarestorage.com", // Cloudflare R2
	".r2.dev",                   // Cloudflare R2 公开域名
	".amazonaws.com",            // AWS S3
	".cloudfront.net",           // AWS CloudFront
	".storage.googleapis.com",   // Google Cloud Storage
	".aliyuncs.com",             // 阿里云 OSS
	".myqcloud.com",             // 腾讯云 COS
	".volces.com",               // 火山引擎 TOS
	".myhuaweicloud.com",        // 华为云 OBS
	".bcebos.com",               // 百度云 BOS
	".blob.core.windows.net",    // Azure Blob Storage
}

func isObjectStorageVideoURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "storage.googleapis.com" {
		return true
	}
	for _, suffix := range objectStorageHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}

// videoURLFromTaskData 从上游任务 JSON 中提取视频直链。
// 形如 /v1/videos/{id}/content 的候选是需要鉴权的代理端点（链式 new-api 上游
// 或自身代理地址），不能当直链匿名抓取，跳过后由调用方走上游 /content。
func videoURLFromTaskData(task *model.Task) string {
	if len(task.Data) == 0 {
		return ""
	}
	// `object` last: meaicc-style relays put the signed direct link there instead
	// of the official "video" literal; non-URL values fail the scheme check below.
	for _, path := range []string{"url", "video_url", "metadata.url", "metadata.origin_video_url", "object"} {
		candidate := strings.TrimSpace(gjson.GetBytes(task.Data, path).String())
		if candidate == "" {
			continue
		}
		parsed, err := url.Parse(candidate)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			continue
		}
		if strings.Contains(parsed.Path, "/v1/videos/") && strings.HasSuffix(parsed.Path, "/content") {
			continue
		}
		return candidate
	}
	return ""
}

// videoProxyError returns a standardized OpenAI-style error response.
func videoProxyError(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
		},
	})
}

func VideoProxy(c *gin.Context) {
	taskID := c.Param("task_id")
	if taskID == "" {
		videoProxyError(c, http.StatusBadRequest, "invalid_request_error", "task_id is required")
		return
	}

	userID := c.GetInt("id")
	var task *model.Task
	var exists bool
	var err error
	taskRecordID := c.GetInt64("video_task_record_id")
	taskOwnerID := c.GetInt("video_task_owner_id")
	if taskOwnerID > 0 && taskOwnerID != userID {
		taskRecordID = 0
	}
	if taskRecordID > 0 {
		if taskOwnerID <= 0 {
			taskOwnerID = userID
		}
		task, exists, err = model.GetByTaskRecordID(taskOwnerID, taskRecordID)
	} else {
		task, exists, err = model.GetByTaskId(userID, taskID)
	}
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to query task %s: %s", taskID, err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to query task")
		return
	}
	if !exists || task == nil {
		videoProxyError(c, http.StatusNotFound, "invalid_request_error", "Task not found")
		return
	}

	if task.Status != model.TaskStatusSuccess {
		videoProxyError(c, http.StatusBadRequest, "invalid_request_error",
			fmt.Sprintf("Task is not completed yet, current status: %s", task.Status))
		return
	}

	channel, err := model.CacheGetChannel(task.ChannelId)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to get channel for task %s: %s", taskID, err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to retrieve channel information")
		return
	}
	baseURL := channel.GetBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	var videoURL string
	proxy := channel.GetSetting().Proxy
	client := service.GetSSRFProtectedHTTPClient()
	if proxy != "" {
		// 渠道代理路径的连接由代理侧建立，无法做拨号时逐 IP 校验，
		// 因此后面对 videoURL 保留请求前的一次性 SSRF 校验。
		client, err = service.GetHttpClientWithProxy(proxy)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to create proxy client for task %s", taskID))
			videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to create proxy client")
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "", nil)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to create request: %s", err.Error()))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to create proxy request")
		return
	}

	// 只有不携带我方凭据的公开直链才允许 302 直连；
	// Gemini/Vertex 的地址可能内嵌 API key 或需要鉴权头，必须走代理。
	allowRedirect := false
	switch channel.Type {
	case constant.ChannelTypeGemini:
		apiKey := task.PrivateData.Key
		if apiKey == "" {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Missing stored API key for Gemini task %s", taskID))
			videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to fetch video content")
			return
		}
		videoURL, err = getGeminiVideoURL(channel, task, apiKey)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to resolve Gemini video content for task %s", taskID))
			videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
			return
		}
		req.Header.Set("x-goog-api-key", apiKey)
	case constant.ChannelTypeVertexAi:
		videoURL, err = getVertexVideoURL(channel, task)
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to resolve Vertex video content for task %s", taskID))
			videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
			return
		}
	case constant.ChannelTypeOpenAI, constant.ChannelTypeSora:
		// 部分 OpenAI 兼容中转不实现 /content，而是在任务 JSON 里直接返回视频直链
		//（如 R2 预签名地址）。有直链时直接代理直链，且不携带渠道密钥；
		// 否则按官方语义走上游 /content。
		videoURL = videoURLFromTaskData(task)
		if videoURL == "" {
			videoURL = fmt.Sprintf("%s/v1/videos/%s/content", baseURL, task.GetUpstreamTaskID())
			req.Header.Set("Authorization", "Bearer "+channel.Key)
		} else {
			allowRedirect = true
		}
	default:
		// Video URL is stored in PrivateData.ResultURL (fallback to FailReason for old data)
		videoURL = task.GetResultURL()
		allowRedirect = true
	}

	videoURL = strings.TrimSpace(videoURL)
	if videoURL == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Video URL is empty for task %s", taskID))
		videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		return
	}

	if strings.HasPrefix(videoURL, "data:") {
		if err := writeVideoDataURL(c, videoURL); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to decode video data for task %s", taskID))
			videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		}
		return
	}

	if allowRedirect && isObjectStorageVideoURL(videoURL) {
		c.Writer.Header().Set("Cache-Control", "private, no-store")
		c.Redirect(http.StatusFound, videoURL)
		return
	}

	var validateErr error
	if proxy == "" {
		validateErr = service.ValidateSSRFProtectedFetchURL(videoURL)
	} else {
		fetchSetting := system_setting.GetFetchSetting()
		validateErr = common.ValidateURLWithFetchSetting(videoURL, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, fetchSetting.ApplyIPFilterForDomain)
	}
	if validateErr != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Video content fetch blocked for task %s", taskID))
		videoProxyError(c, http.StatusForbidden, "server_error", "Failed to fetch video content")
		return
	}

	req.URL, err = url.Parse(videoURL)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to parse video content location for task %s", taskID))
		videoProxyError(c, http.StatusInternalServerError, "server_error", "Failed to create proxy request")
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to fetch video content for task %s", taskID))
		videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Video content upstream returned status %d for task %s", resp.StatusCode, taskID))
		videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		return
	}

	if err := copyVideoProxyHeaders(c.Writer.Header(), resp.Header, videoURL); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Video content upstream returned an unsafe content type for task %s", taskID))
		videoProxyError(c, http.StatusBadGateway, "server_error", "Failed to fetch video content")
		return
	}
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	c.Writer.WriteHeader(resp.StatusCode)
	if _, err = io.Copy(c.Writer, resp.Body); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Failed to stream video content for task %s", taskID))
	}
}

func writeVideoDataURL(c *gin.Context, dataURL string) error {
	parts := strings.SplitN(dataURL, ",", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid data url")
	}

	header := parts[0]
	payload := parts[1]
	if !strings.HasPrefix(header, "data:") {
		return fmt.Errorf("unsupported data url")
	}

	metadata := strings.TrimPrefix(header, "data:")
	if !strings.HasSuffix(strings.ToLower(metadata), ";base64") {
		return fmt.Errorf("unsupported data url")
	}
	mediaType, _, err := mime.ParseMediaType(metadata[:len(metadata)-len(";base64")])
	if err != nil {
		return fmt.Errorf("invalid data url content type")
	}
	mediaType = strings.ToLower(mediaType)
	if !strings.HasPrefix(mediaType, "video/") {
		return fmt.Errorf("unsupported data url content type")
	}

	videoBytes, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		videoBytes, err = base64.RawStdEncoding.DecodeString(payload)
		if err != nil {
			return err
		}
	}

	c.Writer.Header().Set("Content-Type", mediaType)
	c.Writer.Header().Set("Content-Disposition", `attachment; filename="`+videoContentFilename(mediaType)+`"`)
	c.Writer.Header().Set("X-Content-Type-Options", "nosniff")
	c.Writer.Header().Set("Cache-Control", "private, no-store")
	c.Writer.WriteHeader(http.StatusOK)
	_, err = c.Writer.Write(videoBytes)
	return err
}
