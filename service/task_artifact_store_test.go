package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDisabledTaskArtifactStoreHasNoStorageBehavior(t *testing.T) {
	store := GetTaskArtifactStore()
	require.NotNil(t, store)
	assert.False(t, store.Enabled())

	task := &model.Task{TaskID: "task-disabled-store"}
	ref, err := store.Resolve(task, "video")
	require.NoError(t, err)
	assert.Nil(t, ref)

	ref, err = store.Persist(t.Context(), task, types.TaskArtifact{Key: "video", Type: "video"}, strings.NewReader("content"))
	assert.Nil(t, ref)
	assert.ErrorIs(t, err, ErrTaskArtifactStoreDisabled)
	assert.ErrorIs(t, store.Serve(&gin.Context{}, task, &StoredArtifactRef{ObjectKey: "tasks/x/video.mp4"}), ErrTaskArtifactStoreDisabled)
	assert.Same(t, store, GetTaskArtifactStore())
}

func TestS3ArtifactStoreDownloadTarget(t *testing.T) {
	for _, tc := range []struct {
		name         string
		mirror       bool
		pathStyle    bool
		publicURL    string
		storedBucket string
		wantBucket   string
		wantHost     string
		wantRegion   string
	}{
		{name: "original endpoint fallback", storedBucket: "source-artifacts", wantBucket: "source-artifacts", wantHost: "source-artifacts.upload.example.com", wantRegion: "ap-northeast-1"},
		{name: "original public endpoint", publicURL: "https://public.example.com", storedBucket: "source-artifacts", wantBucket: "source-artifacts", wantHost: "source-artifacts.public.example.com", wantRegion: "ap-northeast-1"},
		{name: "mirror source bucket", mirror: true, publicURL: "https://public.example.com", storedBucket: "source-artifacts", wantBucket: "mirror-artifacts", wantHost: "mirror-artifacts.s3.oss-cn-hangzhou.aliyuncs.com", wantRegion: "cn-hangzhou"},
		{name: "mirror implicit source bucket", mirror: true, wantBucket: "mirror-artifacts", wantHost: "mirror-artifacts.s3.oss-cn-hangzhou.aliyuncs.com", wantRegion: "cn-hangzhou"},
		{name: "other historical bucket", mirror: true, publicURL: "https://public.example.com", storedBucket: "legacy-artifacts", wantBucket: "legacy-artifacts", wantHost: "legacy-artifacts.public.example.com", wantRegion: "ap-northeast-1"},
		{name: "mirror path style", mirror: true, pathStyle: true, storedBucket: "source-artifacts", wantBucket: "mirror-artifacts", wantHost: "s3.oss-cn-hangzhou.aliyuncs.com", wantRegion: "cn-hangzhou"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := system_setting.TaskArtifactStoreConfig{
				S3Endpoint: "https://upload.example.com", S3PublicEndpoint: tc.publicURL,
				S3Bucket: "source-artifacts", S3Region: "ap-northeast-1",
				S3AccessKey: "test-access-key", S3SecretKey: "test-secret-key",
				S3PresignTTLSeconds: 3600, S3PathStyle: tc.pathStyle,
			}
			if tc.mirror {
				config.S3DownloadEndpoint = "https://s3.oss-cn-hangzhou.aliyuncs.com"
				config.S3DownloadBucket = "mirror-artifacts"
				config.S3DownloadRegion = "cn-hangzhou"
			}
			store := newS3ArtifactStore(config)
			ref := &StoredArtifactRef{Bucket: tc.storedBucket, ObjectKey: "videos/task 1/片段+1.mp4"}
			original := *ref
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/content?bucket=untrusted&endpoint=https://untrusted.example", nil)
			ctx.Request.Header.Set("Range", "bytes=0-1023")
			require.NoError(t, store.Serve(ctx, nil, ref))

			require.Equal(t, http.StatusFound, recorder.Code)
			location, err := url.Parse(recorder.Header().Get("Location"))
			require.NoError(t, err)
			assert.Equal(t, "https", location.Scheme)
			assert.Equal(t, tc.wantHost, location.Host)
			wantPath := "/" + ref.ObjectKey
			if tc.pathStyle {
				wantPath = "/" + tc.wantBucket + wantPath
			}
			assert.Equal(t, wantPath, location.Path)
			assert.Equal(t, "3600", location.Query().Get("X-Amz-Expires"))
			scope := strings.Split(location.Query().Get("X-Amz-Credential"), "/")
			require.Len(t, scope, 5)
			assert.Equal(t, tc.wantRegion, scope[2])
			assert.Equal(t, "host", location.Query().Get("X-Amz-SignedHeaders"))
			assert.NotEmpty(t, location.Query().Get("X-Amz-Signature"))
			assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
			assert.Equal(t, "no-referrer", recorder.Header().Get("Referrer-Policy"))
			assert.Equal(t, original, *ref)
		})
	}
}

func TestS3ArtifactStoreMirrorKeepsUploadsOnSource(t *testing.T) {
	type uploadedRequest struct {
		method, path, contentType, authorization string
		body                                     []byte
		err                                      error
	}
	uploaded := make(chan uploadedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		uploaded <- uploadedRequest{
			method: r.Method, path: r.URL.Path, body: body, err: err,
			contentType: r.Header.Get("Content-Type"), authorization: r.Header.Get("Authorization"),
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	store := newS3ArtifactStore(system_setting.TaskArtifactStoreConfig{
		S3Endpoint: server.URL, S3PublicEndpoint: "https://public.example.com",
		S3Bucket: "source-artifacts", S3Region: "ap-northeast-1", S3Prefix: "videos",
		S3AccessKey: "test-access-key", S3SecretKey: "test-secret-key", S3PathStyle: true,
		S3DownloadEndpoint: "https://mirror.example.com", S3DownloadBucket: "mirror-artifacts", S3DownloadRegion: "cn-hangzhou",
		S3PresignTTLSeconds: 3600,
	})
	ref, err := store.Persist(t.Context(), &model.Task{TaskID: "task-upload"},
		types.TaskArtifact{Key: "video", Type: "video", MimeType: "video/mp4"}, strings.NewReader("test-video"))
	require.NoError(t, err)
	require.NotNil(t, ref)
	request := <-uploaded
	require.NoError(t, request.err)
	assert.Equal(t, http.MethodPut, request.method)
	assert.Equal(t, "/source-artifacts/"+ref.ObjectKey, request.path)
	assert.Equal(t, []byte("test-video"), request.body)
	assert.Equal(t, "video/mp4", request.contentType)
	assert.Contains(t, request.authorization, "/ap-northeast-1/s3/aws4_request")
	assert.Equal(t, "source-artifacts", ref.Bucket)
	assert.True(t, strings.HasPrefix(ref.ObjectKey, "videos/"))
	assert.True(t, strings.HasSuffix(ref.ObjectKey, "/task-upload/video.mp4"))
}

var _ TaskArtifactStore = disabledArtifactStore{}
