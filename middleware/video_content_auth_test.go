package middleware

import (
	"crypto/hmac"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func issueExpiredVideoContentToken(t *testing.T, taskID string, ownerUserID int) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":            "new-api-video-content",
		"aud":            []string{"new-api-video-content"},
		"token_use":      "video_content",
		"task_id":        taskID,
		"owner_user_id":  ownerUserID,
		"task_record_id": 99,
		"version":        1,
		"iat":            now.Add(-2 * time.Hour).Unix(),
		"nbf":            now.Add(-2 * time.Hour).Unix(),
		"exp":            now.Add(-time.Hour).Unix(),
		"jti":            "expired-video-content-token",
	}
	mac := hmac.New(sha256.New, []byte(common.SessionSecret))
	_, err := mac.Write([]byte("new-api/auth/video_content/v1"))
	require.NoError(t, err)
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(mac.Sum(nil))
	require.NoError(t, err)
	return token
}

func TestVideoContentAuthAcceptsCapabilityOwner(t *testing.T) {
	setupDashboardAuthMiddlewareTest(t)
	gin.SetMode(gin.TestMode)
	token, _, err := service.IssueVideoContentToken("task-public-1", 42, 99)
	require.NoError(t, err)

	router := gin.New()
	router.GET("/v1/videos/:task_id/content", VideoContentAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"id":             c.GetInt("id"),
			"task_record_id": c.GetInt64("video_task_record_id"),
		})
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/videos/task-public-1/content?video_token="+token, nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusOK, response.Code)
	var body struct {
		ID           int   `json:"id"`
		TaskRecordID int64 `json:"task_record_id"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, 42, body.ID)
	assert.Equal(t, int64(99), body.TaskRecordID)
}

func TestVideoContentAuthRejectsInvalidCapabilitiesGenerically(t *testing.T) {
	setupDashboardAuthMiddlewareTest(t)
	gin.SetMode(gin.TestMode)
	validToken, _, err := service.IssueVideoContentToken("task-public-1", 42, 99)
	require.NoError(t, err)
	tamperedToken := validToken[:len(validToken)-2] + "xx"

	tests := []struct {
		name   string
		taskID string
		token  string
	}{
		{name: "missing", taskID: "task-public-1"},
		{name: "malformed", taskID: "task-public-1", token: "not-a-token"},
		{name: "tampered", taskID: "task-public-1", token: tamperedToken},
		{name: "wrong task", taskID: "task-public-2", token: validToken},
		{name: "expired", taskID: "task-public-1", token: issueExpiredVideoContentToken(t, "task-public-1", 42)},
		{name: "duplicate", taskID: "task-public-1", token: validToken + "&video_token=" + validToken},
		{name: "duplicate with empty first value", taskID: "task-public-1", token: "&video_token=" + validToken},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/v1/videos/:task_id/content", VideoContentAuth(), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodGet, "/v1/videos/"+test.taskID+"/content?video_token="+test.token, nil)
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			assert.Equal(t, http.StatusUnauthorized, response.Code)
			assert.JSONEq(t, `{"error":{"message":"Unauthorized","type":"authentication_error"}}`, response.Body.String())
			if test.token != "" {
				assert.NotContains(t, response.Body.String(), test.token)
			}
		})
	}
}

func TestVideoContentAuthMJAPISecretUsesLegacyAuth(t *testing.T) {
	setupDashboardAuthMiddlewareTest(t)
	gin.SetMode(gin.TestMode)
	require.NoError(t, model.DB.AutoMigrate(&model.Token{}))
	const callbackName = "test:video-content-mj-api-secret"
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(gorm.ErrRecordNotFound)
		}
	}))
	t.Cleanup(func() { model.DB.Callback().Query().Remove(callbackName) })
	capability, _, err := service.IssueVideoContentToken("task-public-1", 42, 99)
	require.NoError(t, err)

	router := gin.New()
	router.GET("/v1/videos/:task_id/content", VideoContentAuth(), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	for _, headerValue := range []string{"Bearer invalid-legacy-token", ""} {
		request := httptest.NewRequest(http.MethodGet, "/v1/videos/task-public-1/content?video_token="+capability, nil)
		request.Header["Mj-Api-Secret"] = []string{headerValue}
		response := httptest.NewRecorder()

		router.ServeHTTP(response, request)

		assert.Equal(t, http.StatusUnauthorized, response.Code)
		assert.NotContains(t, response.Body.String(), "authentication_error", "mj-api-secret must delegate to the legacy token-auth response path")
		assert.NotContains(t, response.Body.String(), capability)
	}
}

func TestVideoContentAuthAuthorizationTakesPrecedence(t *testing.T) {
	setupDashboardAuthMiddlewareTest(t)
	gin.SetMode(gin.TestMode)
	user := createMiddlewarePATUser(t, "video-content-session-user", "unused-video-content-pat")
	now := time.Now().Unix()
	session := &model.UserSession{
		SID:             "video-content-session",
		UserID:          user.Id,
		Version:         1,
		UserAuthVersion: user.AuthVersion,
		Status:          model.UserSessionStatusActive,
		RefreshHash:     "video-content-refresh-hash",
		LoginMethod:     "password",
		LastActiveAt:    now,
		ExpiresAt:       now + 3600,
	}
	require.NoError(t, model.CreateUserSession(session))
	accessToken, _, err := service.IssueAccessToken(service.AuthIdentity{
		UserID:          user.Id,
		SessionID:       session.SID,
		UserAuthVersion: session.UserAuthVersion,
		SessionVersion:  session.Version,
	})
	require.NoError(t, err)
	capability, _, err := service.IssueVideoContentToken("task-public-1", 777, 99)
	require.NoError(t, err)

	router := gin.New()
	router.GET("/v1/videos/:task_id/content", VideoContentAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"id":             c.GetInt("id"),
			"task_record_id": c.GetInt64("video_task_record_id"),
		})
	})
	request := httptest.NewRequest(http.MethodGet, "/v1/videos/task-public-1/content?video_token="+capability, nil)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusOK, response.Code)
	var body struct {
		ID           int   `json:"id"`
		TaskRecordID int64 `json:"task_record_id"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &body))
	assert.Equal(t, user.Id, body.ID)
	assert.NotEqual(t, 777, body.ID)
	assert.Zero(t, body.TaskRecordID)

	emptyHeaderRequest := httptest.NewRequest(http.MethodGet, "/v1/videos/task-public-1/content?video_token="+capability, nil)
	emptyHeaderRequest.Header["Authorization"] = []string{""}
	emptyHeaderResponse := httptest.NewRecorder()
	router.ServeHTTP(emptyHeaderResponse, emptyHeaderRequest)
	assert.Equal(t, http.StatusUnauthorized, emptyHeaderResponse.Code, "an explicitly present Authorization header must not fall back to the capability")
}
