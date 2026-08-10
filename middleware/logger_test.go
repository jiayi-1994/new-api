package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestSanitizeLoggedRequestPath(t *testing.T) {
	const secret = "eyJhbGciOiJIUzI1NiJ9.signed-secret"
	path := "/v1/videos/task-1/content?foo=bar&video_token=" + secret + "&encoded=a%2Fb"

	sanitized := sanitizeLoggedRequestPath(path)

	assert.NotContains(t, sanitized, secret)
	assert.Contains(t, sanitized, "video_token=REDACTED")
	assert.Contains(t, sanitized, "foo=bar")
	assert.Contains(t, sanitized, "encoded=a%2Fb")
}

func TestSanitizeLoggedRequestPathRedactsMalformedSeparatorsAndEncodedKey(t *testing.T) {
	const secret = "signed-secret"
	path := "/v1/videos/task-1/content?foo=bar;%76ideo_token=" + secret + "&keep=value"

	sanitized := sanitizeLoggedRequestPath(path)

	assert.NotContains(t, sanitized, secret)
	assert.Contains(t, sanitized, "%76ideo_token=REDACTED")
	assert.Contains(t, sanitized, "foo=bar")
	assert.Contains(t, sanitized, "keep=value")
}

func TestSetUpLoggerRedactsVideoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "eyJhbGciOiJIUzI1NiJ9.signed-secret"
	var output bytes.Buffer
	previousWriter := gin.DefaultWriter
	gin.DefaultWriter = &output
	t.Cleanup(func() { gin.DefaultWriter = previousWriter })

	router := gin.New()
	SetUpLogger(router)
	router.GET("/v1/videos/:task_id/content", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/v1/videos/task-1/content?foo=bar&video_token="+secret, nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assert.NotContains(t, output.String(), secret)
	assert.Contains(t, output.String(), "video_token=REDACTED")
	assert.Contains(t, output.String(), "foo=bar")
}
