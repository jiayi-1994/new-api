package controller

import (
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolutionSnapshotOmitsBillingUnitAndLegacyPerCallFlag(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "video-model",
		PriceData: hosttypes.PriceData{
			ModelPrice: 0.18,
			UsePrice:   true,
			GroupRatioInfo: hosttypes.GroupRatioInfo{
				GroupRatio: 1.25,
			},
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{
				Selection: relaycommon.VideoBillingSelection{
					EffectiveResolution:      "1080p",
					EffectiveDurationSeconds: 8,
					IndependentRatios:        map[string]float64{"video_input": 1.2},
				},
				SelectedResolutionPrice: 0.18,
				QuotaPerUnit:            321_000,
			},
		},
	}

	context := taskBillingContextFromRelayInfo(info)
	require.NotNil(t, context)
	assert.Equal(t, "video_resolution", context.PricingKind)
	assert.False(t, context.PerCallBilling)
	assert.Equal(t, "1080p", context.EffectiveResolution)
	assert.Equal(t, 0.18, context.SelectedResolutionPrice)
	assert.Equal(t, 8, context.EffectiveDurationSeconds)
	assert.Equal(t, 321_000.0, context.QuotaPerUnit)
	assert.Equal(t, map[string]float64{"video_input": 1.2}, context.IndependentRatios)
	assert.Empty(t, context.OtherRatios)

	raw, err := common.Marshal(context)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "billing_unit")
}

func TestResolutionPricingAdminTaskIncludesBillingDetailsAndUserTaskOmitsThem(t *testing.T) {
	originalRedisEnabled := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() { common.RedisEnabled = originalRedisEnabled })
	task := &model.Task{
		ID:       1,
		TaskID:   "suno-task",
		Platform: constant.TaskPlatformSuno,
		PrivateData: model.TaskPrivateData{
			BillingContext: &model.TaskBillingContext{
				PricingKind:              model.TaskPricingKindVideoResolution,
				EffectiveResolution:      "1080p",
				SelectedResolutionPrice:  0.18,
				EffectiveDurationSeconds: 8,
				SettledDurationSeconds:   4,
				IndependentRatios:        map[string]float64{"video_input": 1.2},
			},
		},
	}

	adminTasks, err := tasksToDto([]*model.Task{task}, true)
	require.NoError(t, err)
	require.Len(t, adminTasks, 1)
	require.NotNil(t, adminTasks[0].BillingDetails)
	assert.Equal(t, "1080p", adminTasks[0].BillingDetails.Resolution)
	assert.Equal(t, 0.18, adminTasks[0].BillingDetails.SelectedPricePerSecond)
	assert.Equal(t, 8, adminTasks[0].BillingDetails.SubmittedDurationSeconds)
	assert.Equal(t, 4, adminTasks[0].BillingDetails.EffectiveDurationSeconds)
	assert.Equal(t, map[string]float64{"video_input": 1.2}, adminTasks[0].BillingDetails.IndependentRatios)

	userTasks, err := tasksToDto([]*model.Task{task}, false)
	require.NoError(t, err)
	require.Len(t, userTasks, 1)
	assert.Nil(t, userTasks[0].BillingDetails)
}

func TestTasksToDtoProjectsNonSunoTasksWithoutUpstreamData(t *testing.T) {
	const upstreamTaskID = "upstream/task?X-Amz-Signature=secret"
	const gatewayTaskID = "task_AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	dangerousData, err := common.Marshal(map[string]any{
		"task_id":     upstreamTaskID,
		"url":         "https://storage.example.com/video.mp4?X-Amz-Signature=secret",
		"video_url":   "https://provider.example.com/video.mp4",
		"metadata":    map[string]any{"origin_video_url": "https://r2.example.com/video.mp4?token=secret"},
		"base64_data": "data:video/mp4;base64,secret",
	})
	require.NoError(t, err)

	tasks := []*model.Task{
		{
			ID:          1001,
			TaskID:      upstreamTaskID,
			UserId:      42,
			Platform:    "video-provider",
			Status:      model.TaskStatusSuccess,
			Progress:    "100%",
			FailReason:  "https://legacy.example.com/video.mp4?signature=secret",
			PrivateData: model.TaskPrivateData{ResultURL: "https://storage.example.com/video.mp4?X-Amz-Signature=secret"},
			Properties:  model.Properties{OriginModelName: "public-video-model"},
			Data:        dangerousData,
		},
		{
			ID:          1002,
			TaskID:      gatewayTaskID,
			UserId:      42,
			Platform:    "video-provider",
			Status:      model.TaskStatusInProgress,
			Progress:    "31%",
			PrivateData: model.TaskPrivateData{UpstreamTaskID: "upstream-task-123"},
			Properties:  model.Properties{OriginModelName: "public-video-model"},
			Data:        dangerousData,
		},
		{
			ID:         1003,
			TaskID:     "legacy-upstream-id",
			UserId:     42,
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			Progress:   "64%",
			FailReason: "provider rejected the prompt",
			Properties: model.Properties{OriginModelName: "public-video-model"},
			Data:       dangerousData,
		},
		{
			ID:         1004,
			TaskID:     "legacy/with?reserved=chars",
			UserId:     42,
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: "data:video/mp4;base64,secret",
			Properties: model.Properties{OriginModelName: "public-video-model"},
			Data:       dangerousData,
		},
	}

	results, err := tasksToDto(tasks, false)
	require.NoError(t, err)
	require.Len(t, results, len(tasks))

	success := results[0]
	assert.Empty(t, success.FailReason)
	require.NotEmpty(t, success.ResultURL)
	assert.Regexp(t, `^task_[0-9a-f]{32}$`, success.TaskID)
	assert.True(t, strings.HasPrefix(success.ResultURL, "/v1/videos/"+success.TaskID+"/content?video_token="))
	parsedURL, err := url.Parse(success.ResultURL)
	require.NoError(t, err)
	grant, err := service.ParseVideoContentToken(parsedURL.Query().Get("video_token"), success.TaskID)
	require.NoError(t, err)
	assert.Equal(t, 42, grant.OwnerUserID)
	assert.Equal(t, int64(1001), grant.TaskRecordID)
	assertProjectedVideoData(t, success.Data, success.TaskID, "completed", 100, "public-video-model", success.ResultURL)
	_, err = service.ParseVideoContentToken(parsedURL.Query().Get("video_token"), upstreamTaskID)
	assert.Error(t, err)

	assert.Empty(t, results[1].ResultURL)
	assert.Empty(t, results[1].FailReason)
	assert.Equal(t, gatewayTaskID, results[1].TaskID)
	assertProjectedVideoData(t, results[1].Data, gatewayTaskID, "in_progress", 31, "public-video-model", "")

	assert.Empty(t, results[2].ResultURL)
	assert.Equal(t, "provider rejected the prompt", results[2].FailReason)
	assertProjectedVideoData(t, results[2].Data, results[2].TaskID, "failed", 64, "public-video-model", "")

	assert.Empty(t, results[3].ResultURL)
	assert.Empty(t, results[3].FailReason)
	assertProjectedVideoData(t, results[3].Data, results[3].TaskID, "failed", 0, "public-video-model", "")

	for _, result := range results {
		serialized, err := common.Marshal(result)
		require.NoError(t, err)
		serializedTask := string(serialized)
		assert.NotContains(t, serializedTask, upstreamTaskID)
		assert.NotContains(t, serializedTask, "storage.example.com")
		assert.NotContains(t, serializedTask, "provider.example.com")
		assert.NotContains(t, serializedTask, "r2.example.com")
		assert.NotContains(t, serializedTask, "X-Amz-Signature")
		assert.NotContains(t, serializedTask, "base64_data")
	}
}

func TestTasksToDtoClearsUnsafeFailureReasons(t *testing.T) {
	unsafeReasons := []string{
		"upstream returned https://provider.example.com/error?signature=secret",
		"download failed from //storage.example.com/video.mp4",
		"data:video/mp4;base64,secret",
		"upload failed at oss-cn-hangzhou.aliyuncs.com/object",
		"request to 192.0.2.4:443 failed",
		"upload failed at oss-cn-hangzhou.aliyuncs.com",
		"request to 192.0.2.4 failed",
		"storage.example.com",
		"192.0.2.4",
		"provider returned X-Amz-Signature=secret",
		"provider returned Signature=secret",
		"provider returned OSSAccessKeyId=secret",
		"provider returned api_key=secret",
		"provider returned access_token=secret",
		"provider returned Authorization: Bearer sk-secret",
		"provider returned X-Api-Key=secret",
		"provider returned X-Goog-Api-Key=secret",
		"Incorrect X-Api-Key provided: sk-secret",
		"upstream rejected Bearer sk-secret",
		"Incorrect API key provided: sk-live-secret",
		"download failed at cdn.download",
		"request to storage.video failed",
		"lookup storage.video: no such host",
		"could not resolve cdn.download",
		"certificate is valid for public.example, not cdn.download",
		"TLS handshake with storage.video failed",
		"hostname mismatch: storage.video",
		"TLS handshake with storage.corp failed",
		"hostname mismatch: storage.lan",
		"dial tcp [2001:db8::1]:443: connect: connection refused",
		"model foo.bar unavailable",
	}
	tasks := make([]*model.Task, 0, len(unsafeReasons))
	for i, reason := range unsafeReasons {
		tasks = append(tasks, &model.Task{
			ID:         int64(i + 2000),
			TaskID:     "legacy-upstream-task",
			UserId:     42,
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: reason,
		})
	}

	results, err := tasksToDto(tasks, false)
	require.NoError(t, err)
	for _, result := range results {
		assert.Empty(t, result.FailReason)
		serialized, err := common.Marshal(result)
		require.NoError(t, err)
		assert.NotContains(t, string(serialized), "example.com")
		assert.NotContains(t, string(serialized), "X-Amz-Signature")
	}
}

func TestTasksToDtoPreservesSafeFailureReasons(t *testing.T) {
	safeReasons := []string{
		"request signature is invalid",
		"worker emitted // as a delimiter",
		"X-Amz-Algorithm is unavailable",
		"failed to parse config.json",
		"provider protocol v1.2 is unsupported",
		"API key authentication failed",
		"access token expired",
		"client secret is not configured",
	}
	tasks := make([]*model.Task, 0, len(safeReasons))
	for i, reason := range safeReasons {
		tasks = append(tasks, &model.Task{
			ID:         int64(i + 2200),
			TaskID:     "legacy-upstream-task",
			UserId:     42,
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: reason,
		})
	}

	results, err := tasksToDto(tasks, false)
	require.NoError(t, err)
	for i, result := range results {
		assert.Equal(t, safeReasons[i], result.FailReason)
	}
}

func TestTasksToDtoClearsFailuresLeakingUpstreamTaskID(t *testing.T) {
	tasks := []*model.Task{
		{
			ID:         2401,
			TaskID:     "legacy-upstream-task-id",
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: "provider rejected legacy-upstream-task-id",
		},
		{
			ID:          2402,
			TaskID:      "task_AbCdEfGhIjKlMnOpQrStUvWxYz012345",
			Platform:    "video-provider",
			Status:      model.TaskStatusFailure,
			FailReason:  "provider rejected new-upstream-operation-id",
			PrivateData: model.TaskPrivateData{UpstreamTaskID: "new-upstream-operation-id"},
		},
		{
			ID:         2403,
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: "provider rejected the prompt",
		},
		{
			ID:         2404,
			TaskID:     "whitespace-legacy-upstream-id",
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: "provider rejected whitespace-legacy-upstream-id",
			PrivateData: model.TaskPrivateData{
				UpstreamTaskID: " \t",
			},
		},
	}

	results, err := tasksToDto(tasks, false)
	require.NoError(t, err)
	require.Len(t, results, len(tasks))
	assert.Empty(t, results[0].FailReason)
	assert.Empty(t, results[1].FailReason)
	assert.Equal(t, "provider rejected the prompt", results[2].FailReason)
	assert.Empty(t, results[3].FailReason)
}

func TestTasksToDtoClearsProviderCredentialFailureReasons(t *testing.T) {
	unsafeReasons := []string{
		"download gs://bucket/object",
		"download s3://bucket/object",
		"download r2://bucket/object",
		"provider returned X-Goog-Signature=secret",
		"provider returned X-Amz-Credential=secret",
		"provider returned X-Oss-Signature=secret",
		"provider returned X-Ms-Signature=secret",
		"provider returned OSSAccessKeyId=secret",
		"provider returned GoogleAccessId=secret",
		"provider returned AWSAccessKeyId=secret",
		"provider returned Credential=secret",
		"provider returned sig=secret",
	}
	tasks := make([]*model.Task, 0, len(unsafeReasons))
	for i, reason := range unsafeReasons {
		tasks = append(tasks, &model.Task{
			ID:         int64(i + 2450),
			TaskID:     "legacy-upstream-task",
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: reason,
		})
	}

	results, err := tasksToDto(tasks, false)
	require.NoError(t, err)
	for _, result := range results {
		assert.Empty(t, result.FailReason)
	}
}

func TestTasksToDtoExposesOnlyPublicNonSunoProperties(t *testing.T) {
	const upstreamModel = "provider-internal-model"
	task := &model.Task{
		ID:       2501,
		TaskID:   "legacy-upstream-task",
		Platform: "video-provider",
		Status:   model.TaskStatusInProgress,
		Properties: model.Properties{
			Input:             "safe user input",
			OriginModelName:   "public-video-model",
			UpstreamModelName: upstreamModel,
		},
	}

	results, err := tasksToDto([]*model.Task{task}, false)
	require.NoError(t, err)
	require.Len(t, results, 1)

	serialized, err := common.Marshal(results[0])
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), "upstream_model_name")
	assert.NotContains(t, string(serialized), upstreamModel)
	assert.Contains(t, string(serialized), "safe user input")
	assert.Contains(t, string(serialized), "public-video-model")
}

func TestTasksToDtoRedactsSchemeRelativeFailureURLs(t *testing.T) {
	for i, reason := range []string{
		"download failed from //storage/video?token=secret",
		"download failed from //[2001:db8::1]/video",
	} {
		task := &model.Task{
			ID:         int64(2300 + i),
			TaskID:     "legacy-upstream-task",
			UserId:     42,
			Platform:   "video-provider",
			Status:     model.TaskStatusFailure,
			FailReason: reason,
		}

		results, err := tasksToDto([]*model.Task{task}, false)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Empty(t, results[0].FailReason)
	}
}

func TestTaskIDFilterParsesVideoContentAlias(t *testing.T) {
	publicTaskID, err := service.VideoContentPublicTaskID(4001, "legacy-upstream-task", false)
	require.NoError(t, err)

	taskID, taskRecordID := taskIDFilter(publicTaskID)
	assert.Empty(t, taskID)
	assert.Equal(t, int64(4001), taskRecordID)

	taskID, taskRecordID = taskIDFilter("legacy-upstream-task")
	assert.Equal(t, "legacy-upstream-task", taskID)
	assert.Zero(t, taskRecordID)
}

func TestTasksToDtoAliasesTaskWithoutARealSeparateUpstreamID(t *testing.T) {
	const gatewayTaskID = "task_AbCdEfGhIjKlMnOpQrStUvWxYz012345"
	task := &model.Task{
		ID:          2500,
		TaskID:      gatewayTaskID,
		UserId:      42,
		Platform:    "video-provider",
		Status:      model.TaskStatusInProgress,
		PrivateData: model.TaskPrivateData{UpstreamTaskID: " \t"},
	}

	results, err := tasksToDto([]*model.Task{task}, false)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.NotEqual(t, gatewayTaskID, results[0].TaskID)
	assert.Regexp(t, `^task_[0-9a-f]{32}$`, results[0].TaskID)
}

func TestTasksToDtoPreservesSunoData(t *testing.T) {
	sunoData, err := common.Marshal(map[string]any{"audio_url": "https://audio.example.com/song.mp3", "metadata": map[string]any{"id": "upstream-song"}})
	require.NoError(t, err)
	task := &model.Task{
		ID:       3001,
		TaskID:   "suno-public-task",
		Platform: constant.TaskPlatformSuno,
		Status:   model.TaskStatusSuccess,
		Data:     sunoData,
	}

	results, err := tasksToDto([]*model.Task{task}, false)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.JSONEq(t, string(sunoData), string(results[0].Data))
	assert.Equal(t, task.GetResultURL(), results[0].ResultURL)
}

func assertProjectedVideoData(t *testing.T, data []byte, taskID, status string, progress int, modelName, resultURL string) {
	t.Helper()
	var projection map[string]any
	require.NoError(t, common.Unmarshal(data, &projection))
	expected := map[string]any{
		"object":   "video",
		"status":   status,
		"progress": float64(progress),
		"task_id":  taskID,
		"model":    modelName,
	}
	if resultURL != "" {
		expected["url"] = resultURL
	}
	assert.Equal(t, expected, projection)
}
