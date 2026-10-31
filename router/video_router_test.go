package router

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupVideoRouterFixture stores one completed jimeng text-to-video task owned
// by the user of token "sk-jimengfetch".
func setupVideoRouterFixture(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	previousDB := model.DB
	previousDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	previousSQLitePath := common.SQLitePath
	previousMasterNode := common.IsMasterNode
	previousRedisEnabled := common.RedisEnabled
	common.SQLitePath = t.TempDir() + "/router-video.db"
	common.IsMasterNode = false
	common.RedisEnabled = false
	t.Setenv("SQL_DSN", "")
	require.NoError(t, model.InitDB())
	database := model.DB
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Task{}))
	t.Cleanup(func() {
		sqlDB, closeErr := database.DB()
		require.NoError(t, closeErr)
		require.NoError(t, sqlDB.Close())
		model.DB = previousDB
		common.SetDatabaseTypes(previousDatabaseType, previousLogDatabaseType)
		common.SQLitePath = previousSQLitePath
		common.IsMasterNode = previousMasterNode
		common.RedisEnabled = previousRedisEnabled
	})

	require.NoError(t, database.Create(&model.User{
		Id:          91,
		Username:    "jimeng-fetch-user",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Quota:       100,
		Group:       "default",
		AuthVersion: 1,
	}).Error)
	require.NoError(t, database.Create(&model.Token{
		Id:             1,
		UserId:         91,
		Key:            "jimengfetch",
		Status:         common.TokenStatusEnabled,
		Name:           "jimeng fetch",
		ExpiredTime:    -1,
		UnlimitedQuota: true,
	}).Error)
	require.NoError(t, database.Create(&model.Channel{
		Id:     17,
		Type:   constant.ChannelTypeJimeng,
		Key:    "unused",
		Status: common.ChannelStatusEnabled,
		Name:   "jimeng fetch",
		Models: "jimeng_vgfm_t2v_l20",
		Group:  "default",
	}).Error)

	task := &model.Task{
		CreatedAt: 1710000000,
		UpdatedAt: 1710000060,
		TaskID:    "task_jimeng_public",
		Platform:  constant.TaskPlatform("jimeng"),
		UserId:    91,
		Group:     "default",
		ChannelId: 17,
		Action:    constant.TaskActionTextToVideo,
		Status:    model.TaskStatusSuccess,
		Progress:  "100%",
		PrivateData: model.TaskPrivateData{
			ResultURL: "data:video/mp4;base64,ZGF0YQ==",
		},
	}
	task.SetData(map[string]any{
		"code": 10000,
		"data": map[string]any{
			"status":    "done",
			"task_id":   "jimeng-private-1",
			"video_url": "https://cdn.example/video.mp4",
		},
		"message": "success",
	})
	require.NoError(t, database.Create(task).Error)
}

func TestGetOpenAIVideoRouteRendersJimengTask(t *testing.T) {
	setupVideoRouterFixture(t)

	engine := gin.New()
	SetVideoRouter(engine)
	SetTaskPluginProtocolRouter(engine)
	request := httptest.NewRequest(http.MethodGet, "/v1/videos/task_jimeng_public", nil)
	request.Header.Set("Authorization", "Bearer sk-jimengfetch")
	recorder := httptest.NewRecorder()

	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var response struct {
		ID          string `json:"id"`
		Object      string `json:"object"`
		Status      string `json:"status"`
		Progress    int    `json:"progress"`
		CreatedAt   int64  `json:"created_at"`
		CompletedAt int64  `json:"completed_at"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "task_jimeng_public", response.ID)
	assert.Equal(t, "video", response.Object)
	assert.Equal(t, "completed", response.Status)
	assert.Equal(t, 100, response.Progress)
	assert.Equal(t, int64(1710000000), response.CreatedAt)
	assert.Equal(t, int64(1710000060), response.CompletedAt)
	assert.NotContains(t, recorder.Body.String(), "cdn.example")
	assert.NotContains(t, recorder.Body.String(), "jimeng-private-1")

	for _, testCase := range []struct {
		name          string
		authorization string
		query         string
		wantStatus    int
	}{
		{name: "missing credential rejected", wantStatus: http.StatusUnauthorized},
		{name: "access rejected", query: "?access=not-a-video-credential", wantStatus: http.StatusUnauthorized},
		{name: "bearer accepted", authorization: "Bearer sk-jimengfetch", wantStatus: http.StatusOK},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(
				http.MethodGet,
				"/v1/videos/task_jimeng_public/content"+testCase.query,
				nil,
			)
			if testCase.authorization != "" {
				request.Header.Set("Authorization", testCase.authorization)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			assert.Equal(t, testCase.wantStatus, recorder.Code, recorder.Body.String())
			if testCase.wantStatus == http.StatusOK {
				assert.Equal(t, "data", recorder.Body.String())
			}
		})
	}
}

func TestArkVideoRoutesShareTheOpenAIVideoChain(t *testing.T) {
	setupVideoRouterFixture(t)
	previousAddress, previousPublic := system_setting.ServerAddress, system_setting.TaskPublicAddress
	system_setting.ServerAddress, system_setting.TaskPublicAddress = "https://gateway.example", ""
	t.Cleanup(func() {
		system_setting.ServerAddress, system_setting.TaskPublicAddress = previousAddress, previousPublic
	})

	engine := gin.New()
	SetVideoRouter(engine)
	SetTaskRouter(engine)
	SetTaskPluginProtocolRouter(engine)
	serve := func(method, target, authorization, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, target, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		return recorder
	}
	type arkError struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	t.Run("retrieve renders an Ark task with a credential-free link", func(t *testing.T) {
		recorder := serve(http.MethodGet, "/api/v3/contents/generations/tasks/task_jimeng_public", "Bearer sk-jimengfetch", "")
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		var task struct {
			ID      string `json:"id"`
			Status  string `json:"status"`
			Content struct {
				VideoURL string `json:"video_url"`
			} `json:"content"`
			CreatedAt int64 `json:"created_at"`
			UpdatedAt int64 `json:"updated_at"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &task))
		assert.Equal(t, "task_jimeng_public", task.ID)
		assert.Equal(t, "succeeded", task.Status)
		assert.Equal(t, int64(1710000000), task.CreatedAt)
		assert.Equal(t, int64(1710000060), task.UpdatedAt)
		assert.True(t, strings.HasPrefix(task.Content.VideoURL, "https://gateway.example/v1/tasks/task_jimeng_public/artifacts/video/content?"), task.Content.VideoURL)
		assert.NotContains(t, recorder.Body.String(), "cdn.example")
		assert.NotContains(t, recorder.Body.String(), "jimeng-private-1")

		link, err := url.Parse(task.Content.VideoURL)
		require.NoError(t, err)
		download := serve(http.MethodGet, link.RequestURI(), "", "")
		assert.Equal(t, http.StatusOK, download.Code, download.Body.String())
		assert.Equal(t, "data", download.Body.String())
	})

	for _, tc := range []struct {
		name          string
		method        string
		target        string
		authorization string
		body          string
		wantStatus    int
		wantCode      string
		wantMessage   string
	}{
		{name: "retrieve without credential", method: http.MethodGet, target: "/api/v3/contents/generations/tasks/task_jimeng_public", wantStatus: http.StatusUnauthorized, wantCode: "AuthenticationError"},
		{name: "retrieve unknown task", method: http.MethodGet, target: "/api/v3/contents/generations/tasks/task_missing", authorization: "Bearer sk-jimengfetch", wantStatus: http.StatusBadRequest, wantCode: "task_not_exist"},
		// Authentication runs before the body is read, so an invalid body cannot be probed anonymously.
		{name: "create without credential", method: http.MethodPost, target: "/api/v3/contents/generations/tasks", body: `{"model":"seedance-2.0","duration":-1}`, wantStatus: http.StatusUnauthorized, wantCode: "AuthenticationError"},
		{name: "create with adaptive duration", method: http.MethodPost, target: "/api/v3/contents/generations/tasks", authorization: "Bearer sk-jimengfetch", body: `{"model":"seedance-2.0","duration":-1}`, wantStatus: http.StatusBadRequest, wantCode: "InvalidParameter", wantMessage: "duration must be an integer from 1 to 3600 seconds"},
		{name: "create with form body", method: http.MethodPost, target: "/api/v3/contents/generations/tasks", authorization: "Bearer sk-jimengfetch", wantStatus: http.StatusUnsupportedMediaType, wantCode: "InvalidParameter", wantMessage: "Content-Type must be application/json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := serve(tc.method, tc.target, tc.authorization, tc.body)
			require.Equal(t, tc.wantStatus, recorder.Code, recorder.Body.String())
			var response arkError
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response), recorder.Body.String())
			assert.Equal(t, tc.wantCode, response.Error.Code)
			assert.NotEmpty(t, response.Error.Message)
			if tc.wantMessage != "" {
				assert.Equal(t, tc.wantMessage, response.Error.Message)
			}
		})
	}

	t.Run("create reaches channel selection with the converted model", func(t *testing.T) {
		require.NoError(t, i18n.Init())
		recorder := serve(http.MethodPost, "/api/v3/contents/generations/tasks", "Bearer sk-jimengfetch",
			`{"model":"ark-unserved-model","content":[{"type":"text","text":"a cat"}],"duration":5,"watermark":false}`)
		assert.GreaterOrEqual(t, recorder.Code, http.StatusBadRequest, recorder.Body.String())
		var response arkError
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response), recorder.Body.String())
		assert.Contains(t, response.Error.Message, "ark-unserved-model")
	})
}
