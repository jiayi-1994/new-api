package middleware

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoCreateRequestDebugLogCapturesParametersWithoutSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDebug := common.DebugEnabled
	common.DebugEnabled = true
	t.Cleanup(func() { common.DebugEnabled = previousDebug })
	var output bytes.Buffer
	previousWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &output
	t.Cleanup(func() { gin.DefaultErrorWriter = previousWriter })

	const body = `{"model":"videos-standard","prompt":"private prompt","duration":15,"ratio":"16:9","resolution":"1080p","referenceImages":["https://cdn.example.com/image.png?sig=private-signature"],"parameters":{"resolution":"720P"},"api_key":"private-api-key"}`
	router := gin.New()
	router.POST("/v1/videos", VideoCreateRequestDebugLog(), func(c *gin.Context) {
		got, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		assert.JSONEq(t, body, string(got))
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer private-bearer-token")
	router.ServeHTTP(httptest.NewRecorder(), request)

	logged := output.String()
	assert.Contains(t, logged, "video_create_request")
	assert.Contains(t, logged, `"resolution":"1080p"`)
	assert.Contains(t, logged, `"duration":15`)
	assert.Contains(t, logged, `"ratio":"16:9"`)
	assert.Contains(t, logged, `"referenceImages":1`)
	assert.Contains(t, logged, `"resolution":"720P"`)
	assert.NotContains(t, logged, "private prompt")
	assert.NotContains(t, logged, "private-signature")
	assert.NotContains(t, logged, "private-api-key")
	assert.NotContains(t, logged, "private-bearer-token")
}

func TestVideoCreateRequestDebugLogCapturesMultipartFieldsAndPreservesFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previousDebug := common.DebugEnabled
	common.DebugEnabled = true
	t.Cleanup(func() { common.DebugEnabled = previousDebug })
	var output bytes.Buffer
	previousWriter := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = &output
	t.Cleanup(func() { gin.DefaultErrorWriter = previousWriter })

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "sora-2"))
	require.NoError(t, writer.WriteField("size", "1280x720"))
	require.NoError(t, writer.WriteField("seconds", "8"))
	file, err := writer.CreateFormFile("input_reference", "reference.png")
	require.NoError(t, err)
	_, err = file.Write([]byte("private image bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	router := gin.New()
	router.POST("/v1/videos", VideoCreateRequestDebugLog(), func(c *gin.Context) {
		require.NoError(t, c.Request.ParseMultipartForm(1<<20))
		assert.Equal(t, "1280x720", c.Request.FormValue("size"))
		uploaded, _, err := c.Request.FormFile("input_reference")
		require.NoError(t, err)
		defer uploaded.Close()
		content, err := io.ReadAll(uploaded)
		require.NoError(t, err)
		assert.Equal(t, "private image bytes", string(content))
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/v1/videos", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(httptest.NewRecorder(), request)

	logged := output.String()
	assert.Contains(t, logged, `"size":"1280x720"`)
	assert.Contains(t, logged, `"seconds":"8"`)
	assert.Contains(t, logged, `"input_reference":1`)
	assert.NotContains(t, logged, "private image bytes")
}
