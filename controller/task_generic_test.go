package controller

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/plugins"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupGenericTaskTest(t *testing.T) *model.Task {
	t.Helper()
	originalDB := model.DB
	previousRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	// The dialect harness lets the same fixture run against MySQL and
	// PostgreSQL when TEST_TASK_DB_DIALECT selects them; SQLite stays the default.
	database, _ := openTaskDialectDatabase(t, &model.Task{}, &model.Channel{}, &model.User{})
	model.DB = database
	t.Cleanup(func() {
		model.DB = originalDB
		common.RedisEnabled = previousRedisEnabled
	})

	require.NoError(t, database.Create(&model.User{
		Id: 7, Username: "artifact-owner", Status: common.UserStatusEnabled,
		Role: common.RoleCommonUser, Group: "default",
	}).Error)
	baseURL := "https://example.com"
	require.NoError(t, database.Create(&model.Channel{
		Id: 1, Name: "artifact", Key: "key", BaseURL: &baseURL, Status: common.ChannelStatusEnabled,
	}).Error)
	task := &model.Task{
		TaskID: "task_generic", Platform: "document", UserId: 7, ChannelId: 1,
		Status: model.TaskStatusSuccess, Progress: "100%", SubmitTime: 10, FinishTime: 20,
	}
	require.NoError(t, database.Create(task).Error)
	return task
}

func allowPrivateTaskMediaTest(t *testing.T) {
	t.Helper()
	originalFetchSetting := *system_setting.GetFetchSetting()
	system_setting.GetFetchSetting().EnableSSRFProtection = true
	system_setting.GetFetchSetting().AllowPrivateIp = true
	system_setting.GetFetchSetting().AllowedPorts = []string{"1-65535"}
	t.Cleanup(func() { *system_setting.GetFetchSetting() = originalFetchSetting })
	service.InitHttpClient()
}

func TestGetTaskDoesNotProjectArtifacts(t *testing.T) {
	task := setupGenericTaskTest(t)
	task.FailReason = "https://stale-upstream.invalid/video.mp4"
	task.PrivateData = model.TaskPrivateData{
		ResultURL: "https://private-upstream.invalid/video.mp4",
		Execution: &model.TaskExecutionSnapshot{
			TaskPlugin: &model.TaskPluginSnapshot{Key: "missing-plugin", Name: "Missing"},
		},
	}
	require.NoError(t, model.DB.Save(task).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", 7)
	c.Params = gin.Params{{Key: "key", Value: task.TaskID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/"+task.TaskID, nil)

	GetTask(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, task.TaskID, response["task_id"])
	assert.NotContains(t, response, "artifacts")
	assert.NotContains(t, recorder.Body.String(), "upstream.invalid")
}

func TestGetTaskArtifactsReturnsEmptyForLegacyTask(t *testing.T) {
	task := setupGenericTaskTest(t)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", task.UserId)
	c.Params = gin.Params{{Key: "key", Value: task.TaskID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/"+task.TaskID+"/artifacts", nil)

	GetTaskArtifacts(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	var response struct {
		TaskID    string                 `json:"task_id"`
		Artifacts []taskArtifactResponse `json:"artifacts"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, task.TaskID, response.TaskID)
	assert.Empty(t, response.Artifacts)
}

func TestTaskArtifactAuthorizationKeepsForeignTasksHidden(t *testing.T) {
	task := setupGenericTaskTest(t)

	commonUser, _ := gin.CreateTestContext(httptest.NewRecorder())
	commonUser.Set("id", 8)
	commonUser.Set("role", common.RoleCommonUser)
	_, exists, err := getTaskForArtifactRequest(commonUser, task.TaskID)
	require.NoError(t, err)
	assert.False(t, exists)

	admin, _ := gin.CreateTestContext(httptest.NewRecorder())
	admin.Set("id", 8)
	admin.Set("role", common.RoleAdminUser)
	found, exists, err := getTaskForArtifactRequest(admin, task.TaskID)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, task.TaskID, found.TaskID)

	apiToken, _ := gin.CreateTestContext(httptest.NewRecorder())
	apiToken.Set("id", 8)
	apiToken.Set("role", common.RoleRootUser)
	apiToken.Set("token_id", 99)
	_, exists, err = getTaskForArtifactRequest(apiToken, task.TaskID)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestDashboardTaskArtifactsReturnsLegacyCapabilityWithoutUpstreamURL(t *testing.T) {
	task := setupGenericTaskTest(t)
	previousSecret := common.CryptoSecret
	previousPublicAddress := system_setting.TaskPublicAddress
	common.CryptoSecret = "controller-task-artifact-access-secret"
	system_setting.TaskPublicAddress = "https://gateway.example/prefix"
	t.Cleanup(func() {
		common.CryptoSecret = previousSecret
		system_setting.TaskPublicAddress = previousPublicAddress
	})
	task.Action = constant.TaskActionTextToVideo
	task.FailReason = "https://upstream.invalid/private-video.mp4?signature=secret"
	require.NoError(t, model.DB.Save(task).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", task.UserId)
	c.Set("role", common.RoleCommonUser)
	c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/task/"+task.TaskID+"/artifacts", nil)

	GetDashboardTaskArtifacts(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Artifacts        []taskArtifactResponse `json:"artifacts"`
			LegacyContentURL string                 `json:"legacy_content_url"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Empty(t, response.Data.Artifacts)
	contentURL, err := url.Parse(response.Data.LegacyContentURL)
	require.NoError(t, err)
	assert.Equal(t, "/prefix/v1/tasks/"+task.TaskID+"/artifacts/video/content", contentURL.Path)
	assert.True(t, service.VerifyTaskArtifactAccess(
		contentURL.Query().Get(service.TaskArtifactAccessQueryParameter),
		task.TaskID,
		"video",
	))
	assert.NotContains(t, recorder.Body.String(), "upstream.invalid")
	assert.NotContains(t, recorder.Body.String(), "signature=secret")
}

// Task lists no longer carry the persisted snapshot, so the dashboard reads a
// legacy Suno task's playable clips from the artifacts endpoint instead.
func TestDashboardTaskArtifactsProjectsLegacySunoAudioClips(t *testing.T) {
	task := setupGenericTaskTest(t)
	task.Platform = constant.TaskPlatformSuno
	task.Action = "MUSIC"
	task.Data = []byte(`[{"id":"clip-1","title":"Song","audio_url":"https://cdn.example/a.mp3","metadata":{"tags":"pop","duration":30},"lyric":"private"},{"id":"clip-2","title":"No audio"}]`)
	require.NoError(t, model.DB.Save(task).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", task.UserId)
	c.Set("role", common.RoleCommonUser)
	c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/task/"+task.TaskID+"/artifacts", nil)

	GetDashboardTaskArtifacts(c)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Artifacts        []taskArtifactResponse `json:"artifacts"`
			LegacyAudioClips []map[string]any       `json:"legacy_audio_clips"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Empty(t, response.Data.Artifacts)
	require.Len(t, response.Data.LegacyAudioClips, 1)
	assert.Equal(t, "https://cdn.example/a.mp3", response.Data.LegacyAudioClips[0]["audio_url"])
	assert.Equal(t, "Song", response.Data.LegacyAudioClips[0]["title"])
	assert.NotContains(t, recorder.Body.String(), "private")
}

// Task lists omit the data column; the API DTO keeps the key as null so shape
// checks survive while the payload no longer travels with every row.
func TestTaskListsOmitPersistedSnapshot(t *testing.T) {
	task := setupGenericTaskTest(t)
	task.Data = []byte(`{"data":[{"url":"https://cdn.example/a.png"}]}`)
	require.NoError(t, model.DB.Save(task).Error)

	userTasks := model.TaskGetAllUserTask(task.UserId, 0, 10, model.SyncTaskQueryParams{})
	require.Len(t, userTasks, 1)
	assert.Empty(t, userTasks[0].Data)
	adminTasks := model.TaskGetAllTasks(0, 10, model.SyncTaskQueryParams{})
	require.Len(t, adminTasks, 1)
	assert.Empty(t, adminTasks[0].Data)
	assert.Equal(t, task.TaskID, adminTasks[0].TaskID)

	encoded, err := common.Marshal(tasksToDto(adminTasks, false, common.RoleAdminUser)[0])
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"data":null`)
	assert.NotContains(t, string(encoded), `"result_discarded"`)

	adminTasks[0].PrivateData.ResultDiscarded = true
	encoded, err = common.Marshal(tasksToDto(adminTasks, false, common.RoleAdminUser)[0])
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"result_discarded":true`, "task lists tell the UI that an inline result was not retained")

	stored, exists, err := model.GetByTaskId(task.UserId, task.TaskID)
	require.NoError(t, err)
	require.True(t, exists)
	assert.JSONEq(t, string(task.Data), string(stored.Data), "single-task lookups keep the snapshot")
}

func TestTaskArtifactAccessRequiresActiveOwner(t *testing.T) {
	task := setupGenericTaskTest(t)
	task.Action = constant.TaskActionTextToVideo
	task.FailReason = "https://upstream.invalid/private-video.mp4"
	require.NoError(t, model.DB.Save(task).Error)
	require.NoError(t, model.DB.Model(&model.User{}).
		Where("id = ?", task.UserId).
		Update("status", common.UserStatusDisabled).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(middleware.TaskArtifactAccessContextKey, true)
	c.Params = gin.Params{
		{Key: "key", Value: task.TaskID},
		{Key: "artifact_key", Value: "video"},
	}
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/v1/tasks/"+task.TaskID+"/artifacts/video/content",
		nil,
	)

	TaskArtifactContent(c)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
}

func TestTaskArtifactAccessRejectsAmbiguousHistoricalTaskID(t *testing.T) {
	task := setupGenericTaskTest(t)
	task.Action = constant.TaskActionTextToVideo
	task.FailReason = "https://first-upstream.invalid/video.mp4"
	require.NoError(t, model.DB.Save(task).Error)
	require.NoError(t, model.DB.Create(&model.User{
		Id: 8, Username: "other-artifact-owner", Status: common.UserStatusEnabled,
		Role: common.RoleCommonUser, Group: "default", AffCode: "artifact-owner-8",
	}).Error)
	require.NoError(t, model.DB.Create(&model.Task{
		TaskID: task.TaskID, Platform: task.Platform, UserId: 8, ChannelId: task.ChannelId,
		Action: constant.TaskActionTextToVideo, Status: model.TaskStatusSuccess,
		FailReason: "https://second-upstream.invalid/video.mp4",
	}).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(middleware.TaskArtifactAccessContextKey, true)
	c.Params = gin.Params{
		{Key: "key", Value: task.TaskID},
		{Key: "artifact_key", Value: "video"},
	}
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/v1/tasks/"+task.TaskID+"/artifacts/video/content",
		nil,
	)

	TaskArtifactContent(c)

	assert.Equal(t, http.StatusNotFound, recorder.Code)
	assert.NotContains(t, recorder.Body.String(), "upstream.invalid")
}

func TestLegacyVideoArtifactContentUsesGetResultURL(t *testing.T) {
	task := setupGenericTaskTest(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "bytes=0-3", r.Header.Get("Range"))
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-3/4")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("data"))
	}))
	defer upstream.Close()
	allowPrivateTaskMediaTest(t)

	task.Action = constant.TaskActionTextToVideo
	task.PrivateData.ResultURL = upstream.URL
	task.FailReason = "https://stale.invalid/legacy-fallback.mp4"
	require.NoError(t, model.DB.Save(task).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(middleware.TaskArtifactAccessContextKey, true)
	c.Params = gin.Params{
		{Key: "key", Value: task.TaskID},
		{Key: "artifact_key", Value: "video"},
	}
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/v1/tasks/"+task.TaskID+"/artifacts/video/content",
		nil,
	)
	c.Request.Header.Set("Range", "bytes=0-3")

	TaskArtifactContent(c)

	assert.Equal(t, http.StatusPartialContent, recorder.Code)
	assert.Equal(t, "data", recorder.Body.String())
	assert.Equal(t, "bytes 0-3/4", recorder.Header().Get("Content-Range"))
}

func TestDisabledArtifactStorePreservesPluginUpstreamContent(t *testing.T) {
	task := setupGenericTaskTest(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "provider-key", r.Header.Get("x-goog-api-key"))
		assert.Equal(t, "bytes=0-13", r.Header.Get("Range"))
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-13/14")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("artifact-bytes"))
	}))
	defer upstream.Close()
	allowPrivateTaskMediaTest(t)
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousMemoryCache })

	require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", task.ChannelId).Updates(map[string]any{
		"type":     constant.ChannelTypeGemini,
		"key":      "provider-key",
		"base_url": upstream.URL,
	}).Error)
	task.Platform = constant.TaskPlatform("google")
	task.PrivateData.Execution = &model.TaskExecutionSnapshot{TaskPlugin: &model.TaskPluginSnapshot{
		Key: "google", Name: "Google Veo (Gemini API)", Version: "1.0.0", APIVersion: 1,
	}}
	task.SetData(map[string]any{"response": map[string]any{
		"generateVideoResponse": map[string]any{
			"generatedVideos": []any{map[string]any{"video": map[string]any{"uri": upstream.URL}}},
		},
	}})
	require.NoError(t, model.DB.Save(task).Error)
	require.False(t, service.GetTaskArtifactStore().Enabled())

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(middleware.TaskArtifactAccessContextKey, true)
	c.Params = gin.Params{
		{Key: "key", Value: task.TaskID},
		{Key: "artifact_key", Value: "video"},
	}
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/"+task.TaskID+"/artifacts/video/content", nil)
	c.Request.Header.Set("Range", "bytes=0-13")

	TaskArtifactContent(c)

	assert.Equal(t, http.StatusPartialContent, recorder.Code)
	assert.Equal(t, "artifact-bytes", recorder.Body.String())
	assert.Equal(t, "video/mp4", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "bytes 0-13/14", recorder.Header().Get("Content-Range"))
}

func TestProjectedTaskArtifactValidationRejectsAmbiguousIdentity(t *testing.T) {
	validated, err := validateProjectedTaskArtifacts([]relaychannel.TaskArtifact{
		{Key: "video-main", Type: "video", MimeType: "video/mp4"},
		{Key: "cover.main", Type: "image", MimeType: "image/png"},
	})
	require.NoError(t, err)
	require.Len(t, validated, 2)
	assert.Equal(t, "video-main", validated[0].Key)

	for _, artifacts := range [][]relaychannel.TaskArtifact{
		{{Key: "../video", Type: "video"}},
		{{Key: "video/0", Type: "video"}},
		{{Key: "video-main", Type: "video"}, {Key: "video-main", Type: "image"}},
		{{Key: "video-main", Type: "unknown"}},
		{{Key: "video-main", Type: "video", MimeType: "video/mp4\r\nX-Test: injected"}},
	} {
		_, err := validateProjectedTaskArtifacts(artifacts)
		assert.ErrorIs(t, err, errTaskArtifactPlugin)
	}
}

func TestProxyTaskMediaForwardsRangeAndFiltersResponseHeaders(t *testing.T) {
	task := setupGenericTaskTest(t)
	var receivedRange, receivedAuthorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRange = r.Header.Get("Range")
		receivedAuthorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-3/10")
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Set-Cookie", "provider=secret")
		w.Header().Set("WWW-Authenticate", "Bearer provider")
		w.Header().Set("X-Provider-Secret", "hidden")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("data"))
	}))
	defer upstream.Close()

	allowPrivateTaskMediaTest(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/tasks/task_generic/artifacts/video-main/content", nil)
	c.Request.Header.Set("Range", "bytes=0-3")

	err := proxyTaskMedia(c, task, &relaychannel.TaskContentRequest{
		URL:     upstream.URL,
		Method:  http.MethodGet,
		Headers: map[string]string{"Authorization": "Bearer provider-secret"},
	})

	require.NoError(t, err)
	assert.Equal(t, http.StatusPartialContent, recorder.Code)
	assert.Equal(t, "data", recorder.Body.String())
	assert.Equal(t, "bytes=0-3", receivedRange)
	assert.Equal(t, "Bearer provider-secret", receivedAuthorization)
	assert.Equal(t, "bytes 0-3/10", recorder.Header().Get("Content-Range"))
	assert.Equal(t, "bytes", recorder.Header().Get("Accept-Ranges"))
	assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	assert.Equal(t, "sandbox; default-src 'none'", recorder.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "no-referrer", recorder.Header().Get("Referrer-Policy"))
	assert.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
	assert.Empty(t, recorder.Header().Get("Set-Cookie"))
	assert.Empty(t, recorder.Header().Get("WWW-Authenticate"))
	assert.Empty(t, recorder.Header().Get("X-Provider-Secret"))
}

type taskMediaTestTransport func(*http.Request) (*http.Response, error)

func (transport taskMediaTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestTaskMediaRedirectsPublicStorageWithoutFetchingContent(t *testing.T) {
	task := setupGenericTaskTest(t)
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousMemoryCache })
	allowPrivateTaskMediaTest(t)
	// The transport below serves every URL locally; domain checks remain enabled.
	system_setting.GetFetchSetting().ApplyIPFilterForDomain = false
	system_setting.GetFetchSetting().DomainFilterMode = false
	system_setting.GetFetchSetting().DomainList = nil
	client := service.GetSSRFProtectedHTTPClient()
	previousClient := *client
	t.Cleanup(func() { *client = previousClient })
	var fetched int
	var upstreamAuthorization string
	client.Transport = taskMediaTestTransport(func(request *http.Request) (*http.Response, error) {
		fetched++
		upstreamAuthorization = request.Header.Get("Authorization")
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"video/mp4"}},
			Body: io.NopCloser(strings.NewReader("video-bytes")), Request: request,
		}, nil
	})

	for _, test := range []struct {
		name     string
		url      string
		private  bool
		redirect bool
	}{
		{name: "R2 signed", url: "https://bucket.account.r2.cloudflarestorage.com/video.mp4?X-Amz-Signature=a%2Fb&X-Amz-Expires=3600", redirect: true},
		{name: "R2 public", url: "https://pub-example.r2.dev/video.mp4", redirect: true},
		{name: "S3", url: "https://bucket.s3.us-east-1.amazonaws.com/video.mp4", redirect: true},
		{name: "CloudFront", url: "https://example.cloudfront.net/video.mp4", redirect: true},
		{name: "GCS path style", url: "https://storage.googleapis.com/bucket/video.mp4", redirect: true},
		{name: "GCS bucket", url: "https://bucket.storage.googleapis.com/video.mp4", redirect: true},
		{name: "OSS", url: "https://bucket.oss-cn-hangzhou.aliyuncs.com/video.mp4", redirect: true},
		{name: "COS", url: "https://bucket.cos.ap-guangzhou.myqcloud.com/video.mp4", redirect: true},
		{name: "TOS", url: "https://bucket.tos-cn-beijing.volces.com/video.mp4", redirect: true},
		{name: "OBS", url: "https://bucket.obs.cn-north-4.myhuaweicloud.com/video.mp4", redirect: true},
		{name: "BOS", url: "https://bucket.bj.bcebos.com/video.mp4", redirect: true},
		{name: "Azure", url: "https://account.blob.core.windows.net/videos/video.mp4", redirect: true},
		{name: "unknown host", url: "https://cdn.example.com/video.mp4"},
		{name: "HTTP storage", url: "http://bucket.r2.dev/video.mp4"},
		{name: "spoofed suffix", url: "https://bucket.r2.dev.attacker.example/video.mp4"},
		{name: "missing domain boundary", url: "https://notcloudfront.net/video.mp4"},
		{name: "storage URL only in query", url: "https://example.com/video?url=https://bucket.r2.dev/video.mp4"},
		{name: "authenticated storage", url: "https://bucket.storage.googleapis.com/video.mp4", private: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				t.Run(method, func(t *testing.T) {
					fetched = 0
					upstreamAuthorization = ""
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(method, "/v1/videos/task_generic/content", nil)
					c.Request.Header.Set("Range", "bytes=0-3")
					c.Request.Header.Set("Authorization", "Bearer client-secret")
					descriptor := &relaychannel.TaskContentRequest{URL: test.url, Method: method, Credentialless: !test.private}
					if test.private {
						descriptor.Headers = map[string]string{"Authorization": "Bearer provider-secret"}
					}
					require.NoError(t, proxyTaskMedia(c, task, descriptor))
					c.Writer.WriteHeaderNow()
					assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
					assert.Equal(t, "no-referrer", recorder.Header().Get("Referrer-Policy"))
					if test.redirect {
						assert.Equal(t, http.StatusFound, recorder.Code)
						assert.Equal(t, test.url, recorder.Header().Get("Location"))
						assert.Zero(t, fetched, "CDN video must not pass through the gateway")
						assert.NotContains(t, recorder.Body.String(), "video-bytes")
					} else {
						assert.Equal(t, http.StatusOK, recorder.Code)
						assert.Empty(t, recorder.Header().Get("Location"))
						assert.Equal(t, 1, fetched)
						if test.private {
							assert.Equal(t, "Bearer provider-secret", upstreamAuthorization)
						} else {
							assert.Empty(t, upstreamAuthorization)
						}
					}
					if method == http.MethodHead {
						assert.Empty(t, recorder.Body.String())
					}
				})
			}
		})
	}

	task.Action = constant.TaskActionTextToVideo
	task.PrivateData.ResultURL = "https://bucket.r2.dev/video.mp4?signature=abc%2F123"
	require.NoError(t, model.DB.Save(task).Error)
	for _, endpoint := range []struct {
		name    string
		path    string
		handler gin.HandlerFunc
	}{
		{name: "video endpoint", path: "/v1/videos/:task_id/content", handler: VideoProxy},
		{name: "artifact endpoint", path: "/v1/tasks/:key/artifacts/:artifact_key/content", handler: TaskArtifactContent},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			for _, userID := range []int{task.UserId, task.UserId + 1} {
				fetched = 0
				router := gin.New()
				router.GET(endpoint.path, func(c *gin.Context) {
					c.Set("id", userID)
					endpoint.handler(c)
				})
				path := strings.NewReplacer(":task_id", task.TaskID, ":key", task.TaskID, ":artifact_key", "video").Replace(endpoint.path)
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
				if userID == task.UserId {
					assert.Equal(t, http.StatusFound, recorder.Code)
					assert.Equal(t, task.PrivateData.ResultURL, recorder.Header().Get("Location"))
				} else {
					assert.Equal(t, http.StatusNotFound, recorder.Code)
					assert.Empty(t, recorder.Header().Get("Location"))
					assert.NotContains(t, recorder.Body.String(), task.PrivateData.ResultURL)
				}
				assert.Zero(t, fetched)
			}
		})
	}

	t.Run("Sora plugin uses completed result URLs", func(t *testing.T) {
		require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", task.ChannelId).Updates(map[string]any{
			"type": constant.ChannelTypeOpenAI, "base_url": "https://provider.example", "key": "provider-secret",
		}).Error)
		task.Platform = constant.TaskPlatform("sora")
		task.PrivateData.UpstreamTaskID = "upstream-video"
		task.PrivateData.Execution = &model.TaskExecutionSnapshot{TaskPlugin: &model.TaskPluginSnapshot{
			Key: "sora", Name: "Sora", Version: "1.1.0", APIVersion: 1,
		}}
		previousTransport := client.Transport
		t.Cleanup(func() { client.Transport = previousTransport })
		providerBaseURL := "https://provider.example"
		client.Transport = taskMediaTestTransport(func(request *http.Request) (*http.Response, error) {
			fetched++
			assert.Equal(t, providerBaseURL+"/v1/videos/upstream-video/content", request.URL.String())
			assert.Equal(t, "Bearer provider-secret", request.Header.Get("Authorization"))
			return &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"video/mp4"}},
				Body: io.NopCloser(strings.NewReader("provider-video")), Request: request,
			}, nil
		})
		const directURL = "https://bucket.r2.dev/video.mp4?signature=a%2Fb"
		for _, field := range []string{
			"url", "video_url", "output_url", "metadata.url", "metadata.origin_video_url", "object",
			"same-origin", "same-storage-host", "same-storage-host-other-port", "unrecognized-host",
			"unrecognized-before-cdn", "spoofed-storage-host", "no-url",
		} {
			t.Run(field, func(t *testing.T) {
				fetched = 0
				providerBaseURL = "https://provider.example"
				wantRedirect := true
				data := map[string]any{"status": "completed", "object": "video"}
				switch field {
				case "metadata.url":
					data["metadata"] = map[string]any{"url": directURL}
				case "metadata.origin_video_url":
					data["metadata"] = map[string]any{"origin_video_url": directURL}
				case "same-origin":
					data["video_url"] = "https://provider.example:443/v1/videos/upstream-video/content"
					wantRedirect = false
				case "same-storage-host", "same-storage-host-other-port":
					providerBaseURL = "https://bucket.r2.dev"
					if field == "same-storage-host-other-port" {
						providerBaseURL += ":8443"
					}
					data["video_url"] = directURL
					wantRedirect = false
				case "unrecognized-host":
					data["url"] = "https://files.provider.example/v1/videos/upstream-video/content"
					wantRedirect = false
				case "unrecognized-before-cdn":
					data["url"] = "https://files.provider.example/jobs/upstream-video"
					data["video_url"] = directURL
				case "spoofed-storage-host":
					data["url"] = "https://bucket.r2.dev.example/video.mp4"
					wantRedirect = false
				case "no-url":
					wantRedirect = false
				default:
					data[field] = directURL
				}
				require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", task.ChannelId).Update("base_url", providerBaseURL).Error)
				task.SetData(data)
				require.NoError(t, model.DB.Save(task).Error)
				for _, handler := range []gin.HandlerFunc{VideoProxy, TaskArtifactContent} {
					fetched = 0
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Set("id", task.UserId)
					c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}, {Key: "key", Value: task.TaskID}, {Key: "artifact_key", Value: "video"}}
					c.Request = httptest.NewRequest(http.MethodGet, "/content", nil)
					handler(c)
					c.Writer.WriteHeaderNow()
					if !wantRedirect {
						assert.Equal(t, http.StatusOK, recorder.Code)
						assert.Equal(t, "provider-video", recorder.Body.String())
						assert.Empty(t, recorder.Header().Get("Location"))
						assert.Equal(t, 1, fetched)
					} else {
						assert.Equal(t, http.StatusFound, recorder.Code)
						assert.Equal(t, directURL, recorder.Header().Get("Location"))
						assert.Zero(t, fetched)
					}
				}
			})
		}
	})

	for _, credentialless := range []bool{true, false} {
		name := "public download redirect"
		if !credentialless {
			name = "authenticated download redirect"
		}
		t.Run(name, func(t *testing.T) {
			const cdnURL = "https://bucket.r2.dev/video.mp4?signature=abc%2F123"
			previousTransport := client.Transport
			t.Cleanup(func() { client.Transport = previousTransport })
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				fetched = 0
				var cdnFetched bool
				client.Transport = taskMediaTestTransport(func(request *http.Request) (*http.Response, error) {
					fetched++
					response := &http.Response{
						StatusCode: http.StatusFound, Header: make(http.Header),
						Body: io.NopCloser(strings.NewReader("")), Request: request,
					}
					if request.URL.Host == "provider.example" {
						if credentialless {
							assert.Empty(t, request.Header.Get("Authorization"))
						} else {
							assert.Equal(t, "Bearer provider-secret", request.Header.Get("Authorization"))
						}
						if request.URL.Path == "/content" {
							response.Header.Set("Location", "/download")
						} else {
							response.Header.Set("Location", cdnURL)
						}
					} else {
						cdnFetched = true
						response.StatusCode = http.StatusOK
					}
					return response, nil
				})
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(method, "/content", nil)
				descriptor := &relaychannel.TaskContentRequest{
					URL: "https://provider.example/content", Method: method, Credentialless: credentialless,
				}
				if !credentialless {
					descriptor.Headers = map[string]string{"Authorization": "Bearer provider-secret"}
				}
				err := proxyTaskMedia(c, task, descriptor)
				require.NoError(t, err)
				c.Writer.WriteHeaderNow()
				assert.Equal(t, http.StatusFound, recorder.Code)
				assert.Equal(t, cdnURL, recorder.Header().Get("Location"))
				assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
				assert.Equal(t, "no-referrer", recorder.Header().Get("Referrer-Policy"))
				assert.Equal(t, 2, fetched, "only the provider redirect chain should be fetched")
				assert.False(t, cdnFetched, "neither video bytes nor provider credentials should pass to the CDN")
			}
		})
	}

	t.Run("authenticated same-origin storage redirect stays proxied", func(t *testing.T) {
		fetched = 0
		previousTransport := client.Transport
		t.Cleanup(func() { client.Transport = previousTransport })
		client.Transport = taskMediaTestTransport(func(request *http.Request) (*http.Response, error) {
			fetched++
			assert.Equal(t, "Bearer provider-secret", request.Header.Get("Authorization"))
			response := &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader("video-bytes")), Request: request,
			}
			if request.URL.Path == "/content" {
				response.StatusCode = http.StatusFound
				response.Header.Set("Location", "/video.mp4")
			}
			return response, nil
		})
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/content", nil)
		require.NoError(t, proxyTaskMedia(c, task, &relaychannel.TaskContentRequest{
			URL: "https://bucket.r2.dev/content", Method: http.MethodGet,
			Headers: map[string]string{"Authorization": "Bearer provider-secret"},
		}))
		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.Empty(t, recorder.Header().Get("Location"))
		assert.Equal(t, "video-bytes", recorder.Body.String())
		assert.Equal(t, 2, fetched)
	})

	t.Run("blocked storage domain", func(t *testing.T) {
		system_setting.GetFetchSetting().DomainList = []string{"bucket.r2.dev"}
		previousTransport := client.Transport
		t.Cleanup(func() { client.Transport = previousTransport })
		client.Transport = taskMediaTestTransport(func(request *http.Request) (*http.Response, error) {
			fetched++
			assert.Equal(t, "provider.example", request.URL.Host)
			return &http.Response{
				StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://bucket.r2.dev/video.mp4"}},
				Body: io.NopCloser(strings.NewReader("")), Request: request,
			}, nil
		})
		for index, sourceURL := range []string{"https://bucket.r2.dev/video.mp4", "https://provider.example/content"} {
			fetched = 0
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/content", nil)
			err := proxyTaskMedia(c, task, &relaychannel.TaskContentRequest{
				URL: sourceURL, Method: http.MethodGet, Credentialless: true,
			})
			require.Error(t, err)
			assert.Empty(t, recorder.Header().Get("Location"))
			assert.Equal(t, index, fetched)
		}
	})
}

func TestProxyTaskMediaPassesThroughUnsatisfiedRange(t *testing.T) {
	task := setupGenericTaskTest(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "bytes */10")
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	}))
	defer upstream.Close()

	allowPrivateTaskMediaTest(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/content", nil)

	require.NoError(t, proxyTaskMedia(c, task, &relaychannel.TaskContentRequest{
		URL: upstream.URL, Method: http.MethodGet,
	}))
	assert.Equal(t, http.StatusRequestedRangeNotSatisfiable, recorder.Code)
	assert.Equal(t, "bytes */10", recorder.Header().Get("Content-Range"))
	assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
}

func TestTaskMediaResponseHeaderTimeoutDoesNotTruncateBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(75 * time.Millisecond)
		_, _ = w.Write([]byte("complete-body"))
	}))
	defer upstream.Close()

	request, err := http.NewRequest(http.MethodGet, upstream.URL, nil)
	require.NoError(t, err)
	response, err := doTaskMediaRequest(upstream.Client(), request, 20*time.Millisecond)
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Equal(t, "complete-body", string(body))
}

func TestTaskMediaResponseHeaderTimeoutCancelsBeforeHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(75 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer upstream.Close()

	request, err := http.NewRequest(http.MethodGet, upstream.URL, nil)
	require.NoError(t, err)
	_, err = doTaskMediaRequest(upstream.Client(), request, 10*time.Millisecond)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWriteVideoDataURLStreamsAndSupportsHead(t *testing.T) {
	const dataURL = "data:video/mp4;base64,Y29tcGxldGUtYm9keQ=="

	getRecorder := httptest.NewRecorder()
	getContext, _ := gin.CreateTestContext(getRecorder)
	getContext.Request = httptest.NewRequest(http.MethodGet, "/content", nil)
	require.NoError(t, writeVideoDataURL(getContext, dataURL))
	assert.Equal(t, http.StatusOK, getRecorder.Code)
	assert.Equal(t, "complete-body", getRecorder.Body.String())
	assert.Equal(t, "13", getRecorder.Header().Get("Content-Length"))

	headRecorder := httptest.NewRecorder()
	headContext, _ := gin.CreateTestContext(headRecorder)
	headContext.Request = httptest.NewRequest(http.MethodHead, "/content", nil)
	require.NoError(t, writeVideoDataURL(headContext, dataURL))
	assert.Equal(t, http.StatusOK, headRecorder.Code)
	assert.Empty(t, headRecorder.Body.String())
	assert.Equal(t, "13", headRecorder.Header().Get("Content-Length"))
}

func TestWriteVideoDataURLRejectsOversizedPayloadBeforeDecode(t *testing.T) {
	previousLimit := taskMediaDataURLMaxEncodedBytes
	taskMediaDataURLMaxEncodedBytes = 32
	t.Cleanup(func() { taskMediaDataURLMaxEncodedBytes = previousLimit })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/content", nil)

	err := writeVideoDataURL(c, "data:video/mp4;base64,"+strings.Repeat("A", 64))

	assert.ErrorIs(t, err, errTaskMediaRequestRejected)
	assert.Empty(t, recorder.Header().Get("Content-Type"))
}

func TestProxyTaskMediaAllowsOnlyCredentiallessCrossOriginRedirect(t *testing.T) {
	task := setupGenericTaskTest(t)
	var destinationAuthorization, destinationRange string
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationAuthorization = r.Header.Get("Authorization")
		destinationRange = r.Header.Get("Range")
		_, _ = w.Write([]byte("redirected"))
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer source.Close()
	allowPrivateTaskMediaTest(t)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/content", nil)
	c.Request.Header.Set("Range", "bytes=0-3")

	err := proxyTaskMedia(c, task, &relaychannel.TaskContentRequest{
		URL: source.URL, Method: http.MethodGet, Credentialless: true,
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "redirected", recorder.Body.String())
	assert.Empty(t, destinationAuthorization)
	assert.Equal(t, "bytes=0-3", destinationRange)

	destinationRange = ""
	rejectedRecorder := httptest.NewRecorder()
	rejectedContext, _ := gin.CreateTestContext(rejectedRecorder)
	rejectedContext.Request = httptest.NewRequest(http.MethodGet, "/content", nil)
	err = proxyTaskMedia(rejectedContext, task, &relaychannel.TaskContentRequest{
		URL: source.URL, Method: http.MethodGet,
		Headers: map[string]string{"Authorization": "Bearer provider-secret"},
	})
	var proxyErr *taskMediaProxyError
	require.ErrorAs(t, err, &proxyErr)
	assert.Equal(t, "artifact_request_rejected", proxyErr.code)
	assert.Empty(t, destinationRange)
}

func TestTaskMediaRequestHeaderPolicy(t *testing.T) {
	header := http.Header{}
	require.NoError(t, applyTaskMediaRequestHeaders(header, map[string]string{
		"Authorization": "Bearer provider-secret",
		"X-Signature":   "signed",
	}))
	assert.Equal(t, "Bearer provider-secret", header.Get("Authorization"))
	assert.Equal(t, "signed", header.Get("X-Signature"))

	for _, name := range []string{"Host", "Content-Length", "Accept-Encoding", "Connection", "Proxy-Authorization", "Transfer-Encoding"} {
		t.Run(name, func(t *testing.T) {
			assert.ErrorIs(t, applyTaskMediaRequestHeaders(http.Header{}, map[string]string{name: "bad"}), errTaskMediaRequestRejected)
		})
	}
	assert.ErrorIs(t, applyTaskMediaRequestHeaders(http.Header{}, map[string]string{"X-Test": "bad\r\ninjected"}), errTaskMediaRequestRejected)
}

func TestCredentiallessTaskMediaDescriptorRejectsCredentialsAndBody(t *testing.T) {
	task := setupGenericTaskTest(t)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/content", nil)

	for _, descriptor := range []*relaychannel.TaskContentRequest{
		{URL: "https://example.com/video", Method: http.MethodPost, Credentialless: true},
		{URL: "https://example.com/video", Method: http.MethodGet, Body: []byte("secret"), Credentialless: true},
		{URL: "https://example.com/video", Method: http.MethodGet, Headers: map[string]string{"X-Key": "secret"}, Credentialless: true},
	} {
		err := proxyTaskMedia(c, task, descriptor)
		var proxyErr *taskMediaProxyError
		require.ErrorAs(t, err, &proxyErr)
		assert.Equal(t, "artifact_request_rejected", proxyErr.code)
	}
}

func TestSelfTaskMediaURLGuard(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "https://gateway.example/v1/videos/task-1/content", nil)
	c.Request.Host = "gateway.example"

	selfURL, err := url.Parse("https://gateway.example/v1/videos/task-1/content")
	require.NoError(t, err)
	assert.True(t, isSelfTaskMediaURL(c, selfURL))

	remoteURL, err := url.Parse("https://cdn.example/v1/videos/task-1/content")
	require.NoError(t, err)
	assert.False(t, isSelfTaskMediaURL(c, remoteURL))
	assert.True(t, isTaskMediaFallbackLoop(remoteURL.String(), "task-1"))
	assert.False(t, isTaskMediaFallbackLoop(remoteURL.String(), "task-2"))
}

func TestTaskArtifactSyncStoresVideoAndServesPresignedURL(t *testing.T) {
	task := setupGenericTaskTest(t)
	allowPrivateTaskMediaTest(t)
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = previousMemoryCache })

	var upstreamHits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		if r.URL.Path == "/missing.mp4" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("video-bytes"))
	}))
	defer upstream.Close()

	var storedKey, storedBody string
	bucketServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPut, r.Method)
		body, _ := io.ReadAll(r.Body)
		storedKey, storedBody = r.URL.Path, string(body)
		assert.Equal(t, "video/mp4", r.Header.Get("Content-Type"))
		w.Header().Set("ETag", `"etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer bucketServer.Close()
	restore := service.ConfigureTaskArtifactStore(system_setting.TaskArtifactStoreConfig{
		Mode: system_setting.TaskArtifactStoreModeS3, S3Endpoint: bucketServer.URL, S3Bucket: "artifacts",
		S3PublicEndpoint: "https://public.example.com",
		S3Region:         "us-east-1", S3AccessKey: "ak", S3SecretKey: "sk", S3Prefix: "tasks/v1",
		S3PresignTTLSeconds: 600, S3PathStyle: true, RetentionDays: 30, SyncIntervalSeconds: 60,
	})
	t.Cleanup(restore)
	require.True(t, service.GetTaskArtifactStore().Enabled())

	task.FinishTime = time.Now().Unix()
	task.PrivateData.ResultURL = upstream.URL + "/result.mp4"
	require.NoError(t, model.DB.Save(task).Error)

	summary := runTaskArtifactSyncOnce(context.Background())
	assert.Equal(t, taskArtifactSyncSummary{Scanned: 1, Stored: 1}, summary)
	assert.Equal(t, "/artifacts/tasks/v1/task_generic/video.mp4", storedKey)
	assert.Equal(t, "video-bytes", storedBody)

	var stored model.Task
	require.NoError(t, model.DB.First(&stored, task.ID).Error)
	require.NotNil(t, stored.PrivateData.StoredArtifact)
	assert.Equal(t, "tasks/v1/task_generic/video.mp4", stored.PrivateData.StoredArtifact.ObjectKey)
	assert.Equal(t, int64(len("video-bytes")), stored.PrivateData.StoredArtifact.Size)

	// A second pass is a no-op: nothing is fetched or uploaded again.
	upstreamHits = 0
	assert.Equal(t, taskArtifactSyncSummary{Scanned: 1}, runTaskArtifactSyncOnce(context.Background()))
	assert.Equal(t, 0, upstreamHits)

	requestVideo := func() *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set(middleware.TaskArtifactAccessContextKey, true)
		c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/videos/"+task.TaskID+"/content", nil)
		VideoProxy(c)
		return recorder
	}

	recorder := requestVideo()
	require.Equal(t, http.StatusFound, recorder.Code)
	location, err := url.Parse(recorder.Header().Get("Location"))
	require.NoError(t, err)
	// Uploads went to S3Endpoint (the test bucket); customer links are signed
	// against the public endpoint.
	assert.Equal(t, "https", location.Scheme)
	assert.Equal(t, "public.example.com", location.Host)
	assert.Equal(t, "/artifacts/tasks/v1/task_generic/video.mp4", location.Path)
	assert.NotEmpty(t, location.Query().Get("X-Amz-Signature"))
	assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))

	// Past retention the stored copy is ignored and the upstream path is used.
	require.NoError(t, model.DB.Model(&model.Task{}).Where("id = ?", task.ID).
		Update("finish_time", time.Now().Add(-31*24*time.Hour).Unix()).Error)
	recorder = requestVideo()
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "video-bytes", recorder.Body.String())

	// Unreachable results are retried up to the attempt cap, then left alone.
	require.NoError(t, model.DB.Model(&model.Task{}).Where("id = ?", task.ID).Updates(map[string]any{
		"finish_time":  time.Now().Unix(),
		"private_data": model.TaskPrivateData{ResultURL: upstream.URL + "/missing.mp4"},
	}).Error)
	upstreamHits = 0
	for range model.MaxTaskArtifactStoreAttempts + 1 {
		runTaskArtifactSyncOnce(context.Background())
	}
	assert.Equal(t, model.MaxTaskArtifactStoreAttempts, upstreamHits)
	require.NoError(t, model.DB.First(&stored, task.ID).Error)
	assert.Nil(t, stored.PrivateData.StoredArtifact)
	assert.Equal(t, model.MaxTaskArtifactStoreAttempts, stored.PrivateData.StoreAttempts)
}

func TestTaskArtifactSyncStoresPluginVideos(t *testing.T) {
	for _, tc := range []struct {
		name, plugin, data, path, authorization, googleKey string
		redirect                                           bool
	}{
		{name: "megabyai", plugin: "megabyai", data: `{"video_url":"$MEDIA"}`},
		{name: "meaicc", plugin: "meaicc", data: `{"object":"$MEDIA"}`},
		{name: "alibaba", plugin: "alibaba", data: `{"output":{"video_url":"$MEDIA"}}`},
		{name: "doubao", plugin: "doubao", data: `{"content":{"video_url":"$MEDIA"}}`},
		{name: "google", plugin: "google", data: `{"response":{"generateVideoResponse":{"generatedVideos":[{"video":{"uri":"$MEDIA"}}]}}}`, googleKey: "key"},
		{name: "hailuo_file", plugin: "hailuo", data: `{"file_id":"file-123"}`, path: "/v1/files/download?file_id=file-123", authorization: "Bearer key"},
		{name: "hailuo_h3", plugin: "hailuo", data: `{"task":{"content":{"url":"$MEDIA"}}}`},
		{name: "jimeng", plugin: "jimeng", data: `{"data":{"video_url":"$MEDIA"}}`},
		{name: "kling", plugin: "kling", data: `{"data":{"task_result":{"videos":[{"url":"$MEDIA"}]}}}`},
		{name: "vidu", plugin: "vidu", data: `{"creations":[{"url":"$MEDIA"}]}`},
		{name: "seedance_hjmie_url", plugin: "seedance-hjmie", data: `{"metadata":{"final_video_url":"$MEDIA"}}`},
		{name: "seedance_hjmie_content", plugin: "seedance-hjmie", data: `{}`, path: "/v1/videos/upstream-task/content", authorization: "Bearer key"},
		{name: "paipu_url", plugin: "paipu", data: `{"url":"$MEDIA"}`},
		{name: "paipu_content", plugin: "paipu", data: `{}`, path: "/v1/videos/upstream-task/content", authorization: "Bearer key"},
		{name: "pidoi", plugin: "pidoi", data: `{}`, path: "/v1/videos/upstream-task/content", authorization: "Bearer key"},
		{name: "sora_content", plugin: "sora", data: `{}`, path: "/v1/videos/upstream-task/content", authorization: "Bearer key"},
		{name: "mega_signed_url", plugin: "megabyai", data: `{"data":{"url":"$MEDIA?signature=test-signature&expires=4102444800"}}`, path: "/video.mp4?signature=test-signature&expires=4102444800"},
		{name: "mega_content_redirect", plugin: "megabyai", data: `{"video_url":"$BASE/v1/videos/upstream-task/content.mp4"}`, path: "/v1/videos/upstream-task/content.mp4", authorization: "Bearer key", redirect: true},
		{name: "hjmie_content_redirect", plugin: "seedance-hjmie", data: `{}`, path: "/v1/videos/upstream-task/content", authorization: "Bearer key", redirect: true},
		{name: "paipu_content_redirect", plugin: "paipu", data: `{}`, path: "/v1/videos/upstream-task/content", authorization: "Bearer key", redirect: true},
		{name: "pidoi_content_redirect", plugin: "pidoi", data: `{}`, path: "/v1/videos/upstream-task/content", authorization: "Bearer key", redirect: true},
		{name: "sora_content_redirect", plugin: "sora", data: `{}`, path: "/v1/videos/upstream-task/content", authorization: "Bearer key", redirect: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := setupGenericTaskTest(t)
			allowPrivateTaskMediaTest(t)
			previousMemoryCache, previousRegistry := common.MemoryCacheEnabled, pluginruntime.DefaultRegistry
			common.MemoryCacheEnabled = false
			pluginruntime.DefaultRegistry = pluginruntime.NewRegistry()
			t.Cleanup(func() {
				common.MemoryCacheEnabled, pluginruntime.DefaultRegistry = previousMemoryCache, previousRegistry
			})
			source, err := plugins.Source(tc.plugin)
			require.NoError(t, err)
			plugin, err := pluginruntime.DefaultRegistry.RegisterFactory(source, pluginruntime.Options{})
			require.NoError(t, err)

			var redirectURL string
			if tc.redirect {
				storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					assert.Empty(t, r.Header.Get("Authorization"), "channel credentials must not reach redirected storage")
					assert.Empty(t, r.Header.Get("x-goog-api-key"))
					w.Header().Set("Content-Type", "video/mp4")
					_, _ = w.Write([]byte("plugin-video-bytes"))
				}))
				defer storage.Close()
				redirectURL = storage.URL + "/video.mp4?signature=test-signature"
			}
			var upstreamHits int
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamHits++
				path := tc.path
				if path == "" {
					path = "/video.mp4"
				}
				assert.Equal(t, path, r.URL.RequestURI())
				assert.Equal(t, tc.authorization, r.Header.Get("Authorization"))
				assert.Equal(t, tc.googleKey, r.Header.Get("x-goog-api-key"))
				if tc.redirect {
					http.Redirect(w, r, redirectURL, http.StatusFound)
					return
				}
				w.Header().Set("Content-Type", "video/mp4")
				_, _ = w.Write([]byte("plugin-video-bytes"))
			}))
			defer upstream.Close()
			var storedKey, storedBody string
			bucket := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPut, r.Method)
				body, readErr := io.ReadAll(r.Body)
				assert.NoError(t, readErr)
				storedKey, storedBody = r.URL.Path, string(body)
				w.Header().Set("ETag", `"etag"`)
				w.WriteHeader(http.StatusOK)
			}))
			defer bucket.Close()
			t.Cleanup(service.ConfigureTaskArtifactStore(system_setting.TaskArtifactStoreConfig{
				Mode: system_setting.TaskArtifactStoreModeS3, S3Endpoint: bucket.URL, S3Bucket: "artifacts",
				S3Region: "us-east-1", S3AccessKey: "ak", S3SecretKey: "sk", S3PathStyle: true,
				S3PresignTTLSeconds: 600, RetentionDays: 30, SyncIntervalSeconds: 60,
			}))
			require.NoError(t, model.DB.Model(&model.Channel{}).Where("id = ?", task.ChannelId).
				Updates(map[string]any{"type": constant.ChannelTypeTaskPlugin, "base_url": upstream.URL}).Error)
			task.Platform = constant.TaskPlatform(tc.plugin)
			task.FinishTime = time.Now().Unix()
			task.Data = []byte(strings.NewReplacer("$MEDIA", upstream.URL+"/video.mp4", "$BASE", upstream.URL).Replace(tc.data))
			task.PrivateData = model.TaskPrivateData{
				UpstreamTaskID: "upstream-task",
				ResultURL:      "https://gateway.example/v1/videos/" + task.TaskID + "/content",
				Execution: &model.TaskExecutionSnapshot{TaskPlugin: &model.TaskPluginSnapshot{
					Key: tc.plugin, Version: plugin.Meta.Version,
				}},
			}
			require.NoError(t, model.DB.Save(task).Error)

			assert.Equal(t, taskArtifactSyncSummary{Scanned: 1, Stored: 1}, runTaskArtifactSyncOnce(t.Context()))
			assert.Equal(t, "/artifacts/"+task.TaskID+"/video.mp4", storedKey)
			assert.Equal(t, "plugin-video-bytes", storedBody)
			var stored model.Task
			require.NoError(t, model.DB.First(&stored, task.ID).Error)
			require.NotNil(t, stored.PrivateData.StoredArtifact)
			assert.Equal(t, int64(len("plugin-video-bytes")), stored.PrivateData.StoredArtifact.Size)
			assert.Zero(t, stored.PrivateData.StoreAttempts)
			assert.JSONEq(t, string(task.Data), string(stored.Data))
			assert.Equal(t, taskArtifactSyncSummary{Scanned: 1}, runTaskArtifactSyncOnce(t.Context()))
			assert.Equal(t, 1, upstreamHits, "a stored task must not be downloaded again")
		})
	}
}
