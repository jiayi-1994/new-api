package service

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskRequestBodySnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	media := strings.Repeat("A", 20000)
	longPrompt := strings.Repeat("中", 2000) // 6000 bytes: a long prompt must survive

	var multipartBody bytes.Buffer
	form := multipart.NewWriter(&multipartBody)
	require.NoError(t, form.WriteField("prompt", "a cat"))
	file, err := form.CreateFormFile("input_reference", "cat.png")
	require.NoError(t, err)
	_, err = file.Write(bytes.Repeat([]byte{0x89, 0x00}, 5000))
	require.NoError(t, err)
	require.NoError(t, form.Close())

	cases := []struct {
		name        string
		contentType string
		body        []byte
		want        string
	}{
		{
			name:        "json keeps order, numbers and long prompts",
			contentType: "application/json",
			body:        []byte(`{"model":"sora-2","seed":12345678901234567890,"prompt":"` + longPrompt + `"}`),
			want:        `{"model":"sora-2","seed":12345678901234567890,"prompt":"` + longPrompt + `"}`,
		},
		{
			name:        "json shortens inline media to valid json",
			contentType: "application/json",
			body:        []byte(`{"image":"data:image/png;base64,` + media + `","prompt":"a \"cat\""}`),
			want:        `{"image":"data:image/png;base64,` + media[:256-len("data:image/png;base64,")] + `...[truncated 20022 bytes]","prompt":"a \"cat\""}`,
		},
		{
			name:        "multipart records file parts by name and size",
			contentType: form.FormDataContentType(),
			body:        multipartBody.Bytes(),
			want:        "prompt=a cat\ninput_reference=[file cat.png, 10000 bytes]\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", tc.contentType)
			t.Cleanup(func() { common.CleanupBodyStorage(c) })

			record := TaskRequestBodySnapshot(c)

			require.NotNil(t, record)
			assert.Equal(t, tc.want, record.Body)
			assert.Equal(t, int64(len(tc.body)), record.Size)
			assert.Equal(t, tc.contentType, record.ContentType)
		})
	}
}

func TestTaskRequestBodySnapshotCapsStoredSize(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Many short values defeat per-string shortening; the total cap still holds.
	body := []byte("[" + strings.Repeat(`"`+strings.Repeat("é", 1000)+`",`, 100) + `""]`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	t.Cleanup(func() { common.CleanupBodyStorage(c) })

	record := TaskRequestBodySnapshot(c)

	require.NotNil(t, record)
	assert.LessOrEqual(t, len(record.Body), 65535)
	assert.True(t, strings.HasSuffix(record.Body, "...[truncated]"))
	assert.True(t, utf8.ValidString(record.Body))
}
