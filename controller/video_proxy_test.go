package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCopyVideoProxyHeadersAllowsOnlyDeliveryMetadata(t *testing.T) {
	upstream := http.Header{
		"Content-Type":        []string{"Video/MP4; charset=utf-8"},
		"Content-Length":      []string{"123"},
		"Content-Disposition": []string{`inline; filename="provider-secret.html"`},
		"Accept-Ranges":       []string{"bytes"},
		"Content-Range":       []string{"bytes 0-122/123"},
		"Etag":                []string{`"abc"`},
		"Last-Modified":       []string{"Mon, 10 Aug 2026 10:00:00 GMT"},
		"Location":            []string{"https://storage.example/video.mp4?credential=secret"},
		"Content-Location":    []string{"https://storage.example/video.mp4"},
		"Link":                []string{"<https://storage.example/video.mp4>; rel=canonical"},
		"Set-Cookie":          []string{"provider_session=secret"},
		"X-Provider-Request":  []string{"provider-request-id"},
	}
	destination := make(http.Header)

	require.NoError(t, copyVideoProxyHeaders(destination, upstream, "https://storage.example/video.mp4"))

	assert.Equal(t, "video/mp4", destination.Get("Content-Type"))
	assert.Equal(t, "123", destination.Get("Content-Length"))
	assert.Equal(t, `attachment; filename="video.mp4"`, destination.Get("Content-Disposition"))
	assert.Equal(t, "bytes", destination.Get("Accept-Ranges"))
	assert.Equal(t, "bytes 0-122/123", destination.Get("Content-Range"))
	assert.Equal(t, `"abc"`, destination.Get("ETag"))
	assert.Equal(t, "Mon, 10 Aug 2026 10:00:00 GMT", destination.Get("Last-Modified"))
	assert.Empty(t, destination.Get("Location"))
	assert.Empty(t, destination.Get("Content-Location"))
	assert.Empty(t, destination.Get("Link"))
	assert.Empty(t, destination.Get("Set-Cookie"))
	assert.Empty(t, destination.Get("X-Provider-Request"))
	assert.Equal(t, "nosniff", destination.Get("X-Content-Type-Options"))
}

func TestCopyVideoProxyHeadersUsesMIMEAppropriateDownloadName(t *testing.T) {
	for _, testCase := range []struct {
		contentType string
		filename    string
	}{
		{contentType: "video/webm", filename: "video.webm"},
		{contentType: "video/quicktime", filename: "video.mov"},
		{contentType: "video/x-matroska", filename: "video.mkv"},
		{contentType: "video/unknown", filename: "video.bin"},
		{contentType: "application/octet-stream", filename: "video.bin"},
	} {
		t.Run(testCase.contentType, func(t *testing.T) {
			destination := make(http.Header)
			require.NoError(t, copyVideoProxyHeaders(destination, http.Header{"Content-Type": []string{testCase.contentType}}, ""))
			assert.Equal(t, `attachment; filename="`+testCase.filename+`"`, destination.Get("Content-Disposition"))
		})
	}
}

func TestCopyVideoProxyHeadersRejectsActiveContent(t *testing.T) {
	destination := make(http.Header)
	upstream := http.Header{
		"Content-Type":        []string{"text/html; charset=utf-8"},
		"Content-Disposition": []string{"inline"},
		"X-Provider-Request":  []string{"provider-request-id"},
	}

	err := copyVideoProxyHeaders(destination, upstream, "https://storage.example/video.mp4")

	assert.Error(t, err)
	assert.Empty(t, destination)
}

func TestCopyVideoProxyHeadersNormalizesGenericBinaryAliases(t *testing.T) {
	for _, alias := range []string{
		"binary/octet-stream",
		"application/binary",
		"application/x-octet-stream",
		"application/download",
		"application/x-download",
		"application/force-download",
	} {
		t.Run(alias, func(t *testing.T) {
			destination := make(http.Header)
			require.NoError(t, copyVideoProxyHeaders(destination, http.Header{"Content-Type": []string{alias}}, ""))
			assert.Equal(t, "application/octet-stream", destination.Get("Content-Type"))
			assert.Equal(t, `attachment; filename="video.bin"`, destination.Get("Content-Disposition"))
		})
	}
}

func TestCopyVideoProxyHeadersMapsApplicationMP4ToVideoMP4(t *testing.T) {
	destination := make(http.Header)
	require.NoError(t, copyVideoProxyHeaders(destination, http.Header{"Content-Type": []string{"application/mp4"}}, ""))
	assert.Equal(t, "video/mp4", destination.Get("Content-Type"))
	assert.Equal(t, `attachment; filename="video.mp4"`, destination.Get("Content-Disposition"))
}

func TestCopyVideoProxyHeadersInfersTypeFromURLForGenericBinary(t *testing.T) {
	for _, testCase := range []struct {
		videoURL    string
		contentType string
		filename    string
	}{
		{videoURL: "https://minio.example/bucket/2026/output_abc.mp4", contentType: "video/mp4", filename: "video.mp4"},
		{videoURL: "https://minio.example/clip.m4v?signature=sig", contentType: "video/mp4", filename: "video.mp4"},
		{videoURL: "https://minio.example/clip.webm", contentType: "video/webm", filename: "video.webm"},
		{videoURL: "https://minio.example/clip.MOV", contentType: "video/quicktime", filename: "video.mov"},
		{videoURL: "https://minio.example/clip.mkv", contentType: "video/x-matroska", filename: "video.mkv"},
		{videoURL: "https://minio.example/clip.avi", contentType: "application/octet-stream", filename: "video.bin"},
		{videoURL: "", contentType: "application/octet-stream", filename: "video.bin"},
	} {
		t.Run(testCase.videoURL, func(t *testing.T) {
			destination := make(http.Header)
			require.NoError(t, copyVideoProxyHeaders(destination, http.Header{"Content-Type": []string{"binary/octet-stream"}}, testCase.videoURL))
			assert.Equal(t, testCase.contentType, destination.Get("Content-Type"))
			assert.Equal(t, `attachment; filename="`+testCase.filename+`"`, destination.Get("Content-Disposition"))
		})
	}
}

func TestCopyVideoProxyHeadersURLDoesNotOverrideExplicitVideoType(t *testing.T) {
	destination := make(http.Header)
	require.NoError(t, copyVideoProxyHeaders(destination, http.Header{"Content-Type": []string{"video/webm"}}, "https://minio.example/clip.mp4"))
	assert.Equal(t, "video/webm", destination.Get("Content-Type"))
	assert.Equal(t, `attachment; filename="video.webm"`, destination.Get("Content-Disposition"))
}

func TestCopyVideoProxyHeadersDefaultsMissingContentType(t *testing.T) {
	destination := make(http.Header)

	err := copyVideoProxyHeaders(destination, make(http.Header), "")

	require.NoError(t, err)
	assert.Equal(t, "application/octet-stream", destination.Get("Content-Type"))
	assert.Equal(t, `attachment; filename="video.bin"`, destination.Get("Content-Disposition"))
	assert.Equal(t, "nosniff", destination.Get("X-Content-Type-Options"))
}

func TestWriteVideoDataURLUsesPrivateNoStoreCaching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)

	err := writeVideoDataURL(context, "data:video/mp4;base64,aGVsbG8=")

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "video/mp4", response.Header().Get("Content-Type"))
	assert.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	assert.Equal(t, `attachment; filename="video.mp4"`, response.Header().Get("Content-Disposition"))
	assert.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "hello", response.Body.String())
}

func TestWriteVideoDataURLRejectsActiveContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)

	err := writeVideoDataURL(context, "data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==")

	assert.Error(t, err)
	assert.Empty(t, response.Header())
	assert.Empty(t, response.Body.String())
}

func TestWriteVideoDataURLUsesMIMEAppropriateDownloadName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)

	err := writeVideoDataURL(context, "data:video/quicktime;base64,aGVsbG8=")

	require.NoError(t, err)
	assert.Equal(t, `attachment; filename="video.mov"`, response.Header().Get("Content-Disposition"))
}

func setupVideoProxyTest(t *testing.T) {
	t.Helper()
	previousDB := model.DB
	previousType := common.MainDatabaseType()
	previousMemoryCache := common.MemoryCacheEnabled
	previousFetchSetting := *system_setting.GetFetchSetting()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Task{}))
	model.DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetMainDatabaseType(previousType)
		common.MemoryCacheEnabled = previousMemoryCache
		*system_setting.GetFetchSetting() = previousFetchSetting
	})
}

func createVideoProxyTask(t *testing.T, ownerID int, resultURL string) {
	createVideoProxyTaskForChannel(t, ownerID, resultURL, constant.ChannelTypeUnknown)
}

func createVideoProxyTaskForChannel(t *testing.T, ownerID int, resultURL string, channelType int) {
	t.Helper()
	channel := &model.Channel{Id: 1, Type: channelType, Name: "video-proxy-test", Key: "unused"}
	require.NoError(t, model.DB.Create(channel).Error)
	task := &model.Task{
		TaskID:    "task-public-1",
		UserId:    ownerID,
		ChannelId: channel.Id,
		Status:    model.TaskStatusSuccess,
		PrivateData: model.TaskPrivateData{
			ResultURL: resultURL,
		},
	}
	require.NoError(t, model.DB.Create(task).Error)
}

func serveVideoProxyRequest(userID int) *httptest.ResponseRecorder {
	return serveVideoProxyRequestWithRecordID(userID, 0, "task-public-1")
}

func serveVideoProxyRequestWithRecordID(userID int, taskRecordID int64, pathTaskID string) *httptest.ResponseRecorder {
	router := gin.New()
	router.GET("/v1/videos/:task_id/content", func(c *gin.Context) {
		c.Set("id", userID)
		if taskRecordID > 0 {
			c.Set("video_task_record_id", taskRecordID)
		}
		c.Next()
	}, VideoProxy)
	request := httptest.NewRequest(http.MethodGet, "/v1/videos/"+pathTaskID+"/content", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestVideoProxyKeepsOwnerScopeAndPrivateCaching(t *testing.T) {
	setupVideoProxyTest(t)
	gin.SetMode(gin.TestMode)
	createVideoProxyTask(t, 42, "data:video/mp4;base64,aGVsbG8=")

	ownerResponse := serveVideoProxyRequest(42)
	otherUserResponse := serveVideoProxyRequest(7)

	assert.Equal(t, http.StatusOK, ownerResponse.Code)
	assert.Equal(t, "private, no-store", ownerResponse.Header().Get("Cache-Control"))
	assert.Equal(t, "hello", ownerResponse.Body.String())
	assert.Equal(t, http.StatusNotFound, otherUserResponse.Code)
}

func TestVideoProxySSRFErrorIsGeneric(t *testing.T) {
	setupVideoProxyTest(t)
	gin.SetMode(gin.TestMode)
	createVideoProxyTask(t, 42, "http://127.0.0.1:8080/private/video.mp4?credential=secret")
	fetchSetting := system_setting.GetFetchSetting()
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = false

	response := serveVideoProxyRequest(42)

	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Contains(t, response.Body.String(), "Failed to fetch video content")
	assert.NotContains(t, response.Body.String(), "127.0.0.1")
	assert.NotContains(t, response.Body.String(), "credential")
	assert.NotContains(t, response.Body.String(), "request blocked")
}

func TestVideoProxyProviderResolutionErrorIsGeneric(t *testing.T) {
	setupVideoProxyTest(t)
	gin.SetMode(gin.TestMode)
	createVideoProxyTaskForChannel(t, 42, "", constant.ChannelTypeGemini)

	response := serveVideoProxyRequest(42)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Contains(t, response.Body.String(), "Failed to fetch video content")
	assert.NotContains(t, response.Body.String(), "Gemini")
	assert.NotContains(t, response.Body.String(), "API key")
}

func TestVideoProxyRejectsHTTPActiveContentGenerically(t *testing.T) {
	setupVideoProxyTest(t)
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Disposition", `inline; filename="provider-secret.html"`)
		w.Header().Set("Location", "https://storage.example/video.mp4?credential=secret")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<script>alert(1)</script>"))
	}))
	t.Cleanup(upstream.Close)
	system_setting.GetFetchSetting().EnableSSRFProtection = false
	service.InitHttpClient()
	createVideoProxyTask(t, 42, upstream.URL+"/hostile-video")

	response := serveVideoProxyRequest(42)

	assert.Equal(t, http.StatusBadGateway, response.Code)
	assert.Contains(t, response.Body.String(), "Failed to fetch video content")
	assert.NotContains(t, response.Body.String(), "script")
	assert.NotContains(t, response.Body.String(), upstream.URL)
	assert.NotContains(t, response.Body.String(), "credential")
}

func TestVideoProxyRejectsDataURLActiveContentGenerically(t *testing.T) {
	setupVideoProxyTest(t)
	gin.SetMode(gin.TestMode)
	createVideoProxyTask(t, 42, "data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==")

	response := serveVideoProxyRequest(42)

	assert.Equal(t, http.StatusBadGateway, response.Code)
	assert.Contains(t, response.Body.String(), "Failed to fetch video content")
	assert.NotContains(t, response.Body.String(), "script")
}

func TestVideoProxyOpenAICompatibleUsesDirectURLFromTaskData(t *testing.T) {
	setupVideoProxyTest(t)
	gin.SetMode(gin.TestMode)
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(upstream.Close)
	system_setting.GetFetchSetting().EnableSSRFProtection = false
	service.InitHttpClient()
	channel := &model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Name: "video-proxy-test", Key: "channel-secret"}
	require.NoError(t, model.DB.Create(channel).Error)
	task := &model.Task{
		TaskID:    "task-public-1",
		UserId:    42,
		ChannelId: channel.Id,
		Status:    model.TaskStatusSuccess,
		Data:      json.RawMessage(`{"status":"completed","metadata":{"url":"` + upstream.URL + `/videos/vid_1.mp4"}}`),
	}
	require.NoError(t, model.DB.Create(task).Error)

	response := serveVideoProxyRequest(42)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "hello", response.Body.String())
	assert.Empty(t, gotAuth, "direct URL fetch must not carry the channel key")
}

func TestVideoProxyOpenAICompatibleUsesObjectFieldURL(t *testing.T) {
	// meaicc-style New-API relays put the signed direct video link in the
	// non-standard `object` field and their official /content endpoint is broken
	setupVideoProxyTest(t)
	gin.SetMode(gin.TestMode)
	var gotAuth, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(upstream.Close)
	system_setting.GetFetchSetting().EnableSSRFProtection = false
	service.InitHttpClient()
	channel := &model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Name: "video-proxy-test", Key: "channel-secret"}
	require.NoError(t, model.DB.Create(channel).Error)
	task := &model.Task{
		TaskID:    "task-public-1",
		UserId:    42,
		ChannelId: channel.Id,
		Status:    model.TaskStatusSuccess,
		Data:      json.RawMessage(`{"created_at":1786592438,"id":"wr_up1","object":"` + upstream.URL + `/oss/video.mp4?x-oss-signature=sig","seconds":15,"status":"SUCCEEDED"}`),
	}
	require.NoError(t, model.DB.Create(task).Error)

	response := serveVideoProxyRequest(42)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "hello", response.Body.String())
	assert.Equal(t, "/oss/video.mp4", gotPath)
	assert.Empty(t, gotAuth, "direct URL fetch must not carry the channel key")
}

func TestVideoProxyOpenAISkipsProxyStyleURLAndUsesUpstreamContent(t *testing.T) {
	setupVideoProxyTest(t)
	gin.SetMode(gin.TestMode)
	var gotAuth, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(upstream.Close)
	system_setting.GetFetchSetting().EnableSSRFProtection = false
	service.InitHttpClient()
	baseURL := upstream.URL
	channel := &model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Name: "video-proxy-test", Key: "channel-secret", BaseURL: &baseURL}
	require.NoError(t, model.DB.Create(channel).Error)
	task := &model.Task{
		TaskID:    "task-public-1",
		UserId:    42,
		ChannelId: channel.Id,
		Status:    model.TaskStatusSuccess,
		Data:      json.RawMessage(`{"status":"completed","metadata":{"url":"http://chained-upstream/v1/videos/task_abc/content"}}`),
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: "vid_up",
		},
	}
	require.NoError(t, model.DB.Create(task).Error)

	response := serveVideoProxyRequest(42)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "hello", response.Body.String())
	assert.Equal(t, "Bearer channel-secret", gotAuth)
	assert.Equal(t, "/v1/videos/vid_up/content", gotPath)
}

func TestVideoProxyRedirectsObjectStorageDirectURL(t *testing.T) {
	setupVideoProxyTest(t)
	gin.SetMode(gin.TestMode)
	directURL := "https://bucket.r2.cloudflarestorage.com/videos/vid_1.mp4?X-Amz-Signature=abc"
	channel := &model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, Name: "video-proxy-test", Key: "channel-secret"}
	require.NoError(t, model.DB.Create(channel).Error)
	task := &model.Task{
		TaskID:    "task-public-1",
		UserId:    42,
		ChannelId: channel.Id,
		Status:    model.TaskStatusSuccess,
		Data:      json.RawMessage(`{"status":"completed","metadata":{"url":"` + directURL + `"}}`),
	}
	require.NoError(t, model.DB.Create(task).Error)

	response := serveVideoProxyRequest(42)

	assert.Equal(t, http.StatusFound, response.Code)
	assert.Equal(t, directURL, response.Header().Get("Location"))
	assert.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
}

func TestVideoProxyStillProxiesThirdPartyDirectURL(t *testing.T) {
	setupVideoProxyTest(t)
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("hello"))
	}))
	t.Cleanup(upstream.Close)
	system_setting.GetFetchSetting().EnableSSRFProtection = false
	service.InitHttpClient()
	createVideoProxyTask(t, 42, upstream.URL+"/videos/vid_1.mp4")

	response := serveVideoProxyRequest(42)

	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "hello", response.Body.String())
	assert.Empty(t, response.Header().Get("Location"))
}

func TestIsObjectStorageVideoURL(t *testing.T) {
	for _, testCase := range []struct {
		url      string
		expected bool
	}{
		{url: "https://bucket.r2.cloudflarestorage.com/v.mp4?sig=1", expected: true},
		{url: "https://pub-abc.r2.dev/v.mp4", expected: true},
		{url: "https://bucket.s3.us-east-1.amazonaws.com/v.mp4", expected: true},
		{url: "https://d123.cloudfront.net/v.mp4", expected: true},
		{url: "https://storage.googleapis.com/bucket/v.mp4", expected: true},
		{url: "https://bucket.storage.googleapis.com/v.mp4", expected: true},
		{url: "https://bucket.oss-cn-hangzhou.aliyuncs.com/v.mp4", expected: true},
		{url: "https://bucket.cos.ap-guangzhou.myqcloud.com/v.mp4", expected: true},
		{url: "https://bucket.tos-cn-beijing.volces.com/v.mp4", expected: true},
		{url: "https://bucket.obs.cn-north-4.myhuaweicloud.com/v.mp4", expected: true},
		{url: "https://bucket.bj.bcebos.com/v.mp4", expected: true},
		{url: "https://account.blob.core.windows.net/c/v.mp4", expected: true},
		{url: "http://bucket.r2.cloudflarestorage.com/v.mp4", expected: false},
		{url: "https://update.asiot.top/videos/v.mp4", expected: false},
		{url: "https://evil.example.com/?fake=.r2.cloudflarestorage.com", expected: false},
		{url: "https://r2.cloudflarestorage.com.evil.example/v.mp4", expected: false},
	} {
		t.Run(testCase.url, func(t *testing.T) {
			assert.Equal(t, testCase.expected, isObjectStorageVideoURL(testCase.url))
		})
	}
}

func TestVideoProxyCapabilityUsesOwnerScopedTaskRecordID(t *testing.T) {
	setupVideoProxyTest(t)
	gin.SetMode(gin.TestMode)
	createVideoProxyTask(t, 42, "data:video/mp4;base64,aGVsbG8=")
	var task model.Task
	require.NoError(t, model.DB.Where("user_id = ?", 42).First(&task).Error)
	require.NoError(t, model.DB.Model(&task).Update("task_id", "legacy-upstream-task-id").Error)

	ownerResponse := serveVideoProxyRequestWithRecordID(42, task.ID, "task_safe_public_id")
	otherOwnerResponse := serveVideoProxyRequestWithRecordID(7, task.ID, "task_safe_public_id")

	assert.Equal(t, http.StatusOK, ownerResponse.Code)
	assert.Equal(t, "hello", ownerResponse.Body.String())
	assert.Equal(t, http.StatusNotFound, otherOwnerResponse.Code)
}

func TestVideoProxyCapabilityRecordDoesNotOverrideAuthenticatedOwner(t *testing.T) {
	setupVideoProxyTest(t)
	gin.SetMode(gin.TestMode)
	createVideoProxyTask(t, 42, "data:video/mp4;base64,aGVsbG8=")
	var task model.Task
	require.NoError(t, model.DB.Where("user_id = ?", 42).First(&task).Error)

	router := gin.New()
	router.GET("/v1/videos/:task_id/content", func(c *gin.Context) {
		c.Set("id", 7)
		c.Set("video_task_record_id", task.ID)
		c.Set("video_task_owner_id", 42)
		c.Next()
	}, VideoProxy)
	request := httptest.NewRequest(http.MethodGet, "/v1/videos/task_safe_public_id/content", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNotFound, response.Code)
}

func TestVideoContentRouteCapabilityEndToEnd(t *testing.T) {
	setupVideoProxyTest(t)
	gin.SetMode(gin.TestMode)
	createVideoProxyTask(t, 42, "data:video/mp4;base64,aGVsbG8=")
	var task model.Task
	require.NoError(t, model.DB.Where("user_id = ?", 42).First(&task).Error)
	require.NoError(t, model.DB.Model(&task).Update("task_id", "legacy-upstream-task-id").Error)
	publicTaskID, err := service.VideoContentPublicTaskID(task.ID, "legacy-upstream-task-id", false)
	require.NoError(t, err)
	validToken, _, err := service.IssueVideoContentToken(publicTaskID, 42, task.ID)
	require.NoError(t, err)
	wrongOwnerToken, _, err := service.IssueVideoContentToken(publicTaskID, 7, task.ID)
	require.NoError(t, err)

	router := gin.New()
	router.GET("/v1/videos/:task_id/content", middleware.VideoContentAuth(), VideoProxy)
	requestContent := func(pathTaskID, token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/v1/videos/"+pathTaskID+"/content?video_token="+url.QueryEscape(token), nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}

	validResponse := requestContent(publicTaskID, validToken)
	swappedPathResponse := requestContent(publicTaskID+"-swapped", validToken)
	wrongOwnerResponse := requestContent(publicTaskID, wrongOwnerToken)

	assert.Equal(t, http.StatusOK, validResponse.Code)
	assert.Equal(t, "hello", validResponse.Body.String())
	assert.Equal(t, http.StatusUnauthorized, swappedPathResponse.Code)
	assert.Equal(t, http.StatusNotFound, wrongOwnerResponse.Code)
	assert.NotEqual(t, "hello", wrongOwnerResponse.Body.String())
}
