package system_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTaskArtifactStoreConfig(t *testing.T) {
	valid := TaskArtifactStoreConfig{
		Mode:                TaskArtifactStoreModeS3,
		S3Endpoint:          "https://objects.example.com/storage",
		S3Bucket:            "task-artifacts",
		S3Region:            "us-east-1",
		S3AccessKey:         "access-key",
		S3SecretKey:         "secret-key",
		S3Prefix:            "tasks/v1/",
		S3PresignTTLSeconds: 900,
		RetentionDays:       DefaultTaskArtifactStoreRetentionDays,
		SyncIntervalSeconds: DefaultTaskArtifactStoreSyncIntervalSecs,
	}
	require.NoError(t, ValidateTaskArtifactStoreConfig(valid))
	valid.S3DownloadEndpoint = "https://mirror.example.com"
	valid.S3DownloadBucket = "mirror-artifacts"
	valid.S3DownloadRegion = "cn-hangzhou"
	require.NoError(t, ValidateTaskArtifactStoreConfig(valid))
	require.NoError(t, ValidateTaskArtifactStoreConfig(TaskArtifactStoreConfig{
		Mode:                TaskArtifactStoreModeUpstream,
		S3PresignTTLSeconds: DefaultTaskArtifactStorePresignTTLSeconds,
		RetentionDays:       DefaultTaskArtifactStoreRetentionDays,
		SyncIntervalSeconds: DefaultTaskArtifactStoreSyncIntervalSecs,
	}))

	tests := []struct {
		name   string
		mutate func(*TaskArtifactStoreConfig)
		match  string
	}{
		{name: "mode", mutate: func(config *TaskArtifactStoreConfig) { config.Mode = "filesystem" }, match: "unsupported mode"},
		{name: "endpoint scheme", mutate: func(config *TaskArtifactStoreConfig) { config.S3Endpoint = "ftp://objects.example.com" }, match: "http or https"},
		{name: "endpoint credentials", mutate: func(config *TaskArtifactStoreConfig) { config.S3Endpoint = "https://user:pass@objects.example.com" }, match: "without userinfo"},
		{name: "endpoint query", mutate: func(config *TaskArtifactStoreConfig) { config.S3Endpoint = "https://objects.example.com?token=secret" }, match: "query or fragment"},
		{name: "public endpoint scheme", mutate: func(config *TaskArtifactStoreConfig) { config.S3PublicEndpoint = "ftp://objects.example.com" }, match: "public endpoint must use http or https"},
		{name: "missing download endpoint", mutate: func(config *TaskArtifactStoreConfig) { config.S3DownloadEndpoint = "" }, match: "must be configured together"},
		{name: "missing download bucket", mutate: func(config *TaskArtifactStoreConfig) { config.S3DownloadBucket = "" }, match: "must be configured together"},
		{name: "missing download region", mutate: func(config *TaskArtifactStoreConfig) { config.S3DownloadRegion = "" }, match: "must be configured together"},
		{name: "download endpoint scheme", mutate: func(config *TaskArtifactStoreConfig) { config.S3DownloadEndpoint = "ftp://mirror.example.com" }, match: "download endpoint must use http or https"},
		{name: "download endpoint credentials", mutate: func(config *TaskArtifactStoreConfig) {
			config.S3DownloadEndpoint = "https://user:pass@mirror.example.com"
		}, match: "without userinfo"},
		{name: "download endpoint query", mutate: func(config *TaskArtifactStoreConfig) {
			config.S3DownloadEndpoint = "https://mirror.example.com?token=secret"
		}, match: "query or fragment"},
		{name: "download endpoint fragment", mutate: func(config *TaskArtifactStoreConfig) { config.S3DownloadEndpoint = "https://mirror.example.com#video" }, match: "query or fragment"},
		{name: "download bucket", mutate: func(config *TaskArtifactStoreConfig) { config.S3DownloadBucket = "Invalid_Bucket" }, match: "download bucket syntax"},
		{name: "download IP bucket", mutate: func(config *TaskArtifactStoreConfig) { config.S3DownloadBucket = "192.168.1.1" }, match: "download bucket syntax"},
		{name: "download region", mutate: func(config *TaskArtifactStoreConfig) { config.S3DownloadRegion = "bad region" }, match: "download region syntax"},
		{name: "bucket", mutate: func(config *TaskArtifactStoreConfig) { config.S3Bucket = "Invalid_Bucket" }, match: "bucket syntax"},
		{name: "IP bucket", mutate: func(config *TaskArtifactStoreConfig) { config.S3Bucket = "192.168.1.1" }, match: "bucket syntax"},
		{name: "region", mutate: func(config *TaskArtifactStoreConfig) { config.S3Region = "bad region" }, match: "region syntax"},
		{name: "access key", mutate: func(config *TaskArtifactStoreConfig) { config.S3AccessKey = " access-key" }, match: "access key syntax"},
		{name: "secret key", mutate: func(config *TaskArtifactStoreConfig) { config.S3SecretKey = "secret\nkey" }, match: "secret key syntax"},
		{name: "prefix root", mutate: func(config *TaskArtifactStoreConfig) { config.S3Prefix = "/tasks" }, match: "prefix syntax"},
		{name: "prefix traversal", mutate: func(config *TaskArtifactStoreConfig) { config.S3Prefix = "tasks/../private" }, match: "dot segments"},
		{name: "TTL zero", mutate: func(config *TaskArtifactStoreConfig) { config.S3PresignTTLSeconds = 0 }, match: "presign TTL"},
		{name: "TTL too long", mutate: func(config *TaskArtifactStoreConfig) {
			config.S3PresignTTLSeconds = MaxTaskArtifactStorePresignTTLSeconds + 1
		}, match: "presign TTL"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			config := valid
			testCase.mutate(&config)
			assert.ErrorContains(t, ValidateTaskArtifactStoreConfig(config), testCase.match)
		})
	}
}

func TestLoadTaskArtifactStoreConfigModes(t *testing.T) {
	t.Setenv(TaskArtifactStoreS3DownloadEndpointEnv, "")
	t.Setenv(TaskArtifactStoreS3DownloadBucketEnv, "")
	t.Setenv(TaskArtifactStoreS3DownloadRegionEnv, "")
	t.Setenv(TaskArtifactStoreModeEnv, "filesystem")
	t.Setenv(TaskArtifactStoreS3PresignTTLEnv, "900")
	config := LoadTaskArtifactStoreConfig()
	assert.Equal(t, TaskArtifactStoreModeUpstream, config.Mode)

	t.Setenv(TaskArtifactStoreModeEnv, TaskArtifactStoreModeS3)
	t.Setenv(TaskArtifactStoreS3EndpointEnv, "https://objects.example.com")
	t.Setenv(TaskArtifactStoreS3BucketEnv, "task-artifacts")
	t.Setenv(TaskArtifactStoreS3RegionEnv, "us-east-1")
	t.Setenv(TaskArtifactStoreS3AccessKeyEnv, "access-key")
	t.Setenv(TaskArtifactStoreS3SecretKeyEnv, "secret-key")
	t.Setenv(TaskArtifactStoreS3PrefixEnv, "tasks/v1")
	t.Setenv(TaskArtifactStoreS3PresignTTLEnv, "600")
	config = LoadTaskArtifactStoreConfig()

	assert.Equal(t, TaskArtifactStoreModeS3, config.Mode)
	assert.Equal(t, "https://objects.example.com", config.S3Endpoint)
	assert.Empty(t, config.S3PublicEndpoint)
	assert.Equal(t, 600, config.S3PresignTTLSeconds)

	t.Setenv(TaskArtifactStoreS3PublicEndpointEnv, "https://public.example.com")
	config = LoadTaskArtifactStoreConfig()
	assert.Equal(t, "https://objects.example.com", config.S3Endpoint)
	assert.Equal(t, "https://public.example.com", config.S3PublicEndpoint)
	t.Setenv(TaskArtifactStoreS3DownloadEndpointEnv, "https://mirror.example.com")
	t.Setenv(TaskArtifactStoreS3DownloadBucketEnv, "mirror-artifacts")
	t.Setenv(TaskArtifactStoreS3DownloadRegionEnv, "cn-hangzhou")
	config = LoadTaskArtifactStoreConfig()
	assert.Equal(t, TaskArtifactStoreModeS3, config.Mode)
	assert.Equal(t, "https://mirror.example.com", config.S3DownloadEndpoint)
	assert.Equal(t, "mirror-artifacts", config.S3DownloadBucket)
	assert.Equal(t, "cn-hangzhou", config.S3DownloadRegion)
	assert.Equal(t, "task-artifacts", config.S3Bucket)
	assert.Equal(t, "us-east-1", config.S3Region)
	assert.Equal(t, "https://public.example.com", config.S3PublicEndpoint)

	t.Setenv(TaskArtifactStoreS3DownloadRegionEnv, "")
	assert.Equal(t, TaskArtifactStoreModeUpstream, LoadTaskArtifactStoreConfig().Mode)
	t.Setenv(TaskArtifactStoreS3DownloadEndpointEnv, "")
	t.Setenv(TaskArtifactStoreS3DownloadBucketEnv, "")
	config = LoadTaskArtifactStoreConfig()
	assert.Equal(t, TaskArtifactStoreModeS3, config.Mode)
	assert.Empty(t, config.S3DownloadEndpoint)
	assert.Empty(t, config.S3DownloadBucket)
	assert.Empty(t, config.S3DownloadRegion)
	t.Setenv(TaskArtifactStoreS3PublicEndpointEnv, "")
	assert.Equal(t, DefaultTaskArtifactStoreRetentionDays, config.RetentionDays)
	assert.Equal(t, DefaultTaskArtifactStoreSyncIntervalSecs, config.SyncIntervalSeconds)

	t.Setenv(TaskArtifactStoreS3BucketEnv, "")
	config = LoadTaskArtifactStoreConfig()
	assert.Equal(t, TaskArtifactStoreModeUpstream, config.Mode)

	t.Setenv(TaskArtifactStoreS3BucketEnv, "task-artifacts")
	t.Setenv(TaskArtifactStoreRetentionEnv, "0")
	config = LoadTaskArtifactStoreConfig()
	assert.Equal(t, TaskArtifactStoreModeUpstream, config.Mode)
}
