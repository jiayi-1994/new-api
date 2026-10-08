package system_setting

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
)

const (
	TaskArtifactStoreModeUpstream = "upstream"
	TaskArtifactStoreModeS3       = "s3"

	DefaultTaskArtifactStorePresignTTLSeconds = 900
	MaxTaskArtifactStorePresignTTLSeconds     = 7 * 24 * 60 * 60
	DefaultTaskArtifactStoreRetentionDays     = 30
	DefaultTaskArtifactStoreSyncIntervalSecs  = 60
	MinTaskArtifactStoreSyncIntervalSecs      = 10
)

const (
	TaskArtifactStoreModeEnv       = "TASK_ARTIFACT_STORE_MODE"
	TaskArtifactStoreS3EndpointEnv = "TASK_ARTIFACT_STORE_S3_ENDPOINT"
	// Optional endpoint used only to presign customer download URLs, so uploads
	// can go through a private (e.g. OSS -internal) endpoint while customers
	// still receive publicly reachable links. Defaults to S3Endpoint.
	TaskArtifactStoreS3PublicEndpointEnv = "TASK_ARTIFACT_STORE_S3_PUBLIC_ENDPOINT"
	TaskArtifactStoreS3BucketEnv         = "TASK_ARTIFACT_STORE_S3_BUCKET"
	TaskArtifactStoreS3RegionEnv         = "TASK_ARTIFACT_STORE_S3_REGION"
	TaskArtifactStoreS3AccessKeyEnv      = "TASK_ARTIFACT_STORE_S3_ACCESS_KEY"
	TaskArtifactStoreS3SecretKeyEnv      = "TASK_ARTIFACT_STORE_S3_SECRET_KEY"
	TaskArtifactStoreS3PrefixEnv         = "TASK_ARTIFACT_STORE_S3_PREFIX"
	TaskArtifactStoreS3PresignTTLEnv     = "TASK_ARTIFACT_STORE_S3_PRESIGN_TTL"
	TaskArtifactStoreS3PathStyleEnv      = "TASK_ARTIFACT_STORE_S3_PATH_STYLE"
	TaskArtifactStoreRetentionEnv        = "TASK_ARTIFACT_STORE_RETENTION_DAYS"
	TaskArtifactStoreSyncIntervalEnv     = "TASK_ARTIFACT_STORE_SYNC_INTERVAL"
)

var (
	taskArtifactStoreBucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	taskArtifactStoreRegionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// TaskArtifactStoreConfig is the startup-only storage configuration. Mode
// "upstream" (default) keeps proxying provider URLs; mode "s3" additionally
// copies finished artifacts to an S3-compatible bucket and serves them from
// there while they are within RetentionDays.
type TaskArtifactStoreConfig struct {
	Mode                string
	S3Endpoint          string
	S3PublicEndpoint    string
	S3Bucket            string
	S3Region            string
	S3AccessKey         string
	S3SecretKey         string
	S3Prefix            string
	S3PresignTTLSeconds int
	S3PathStyle         bool
	RetentionDays       int
	SyncIntervalSeconds int
}

// LoadTaskArtifactStoreConfig reads and validates startup-only configuration.
// Any validation failure falls back to upstream mode so a misconfigured store
// never blocks video delivery.
func LoadTaskArtifactStoreConfig() TaskArtifactStoreConfig {
	config := TaskArtifactStoreConfig{
		Mode:                common.GetEnvOrDefaultString(TaskArtifactStoreModeEnv, TaskArtifactStoreModeUpstream),
		S3Endpoint:          common.GetEnvOrDefaultString(TaskArtifactStoreS3EndpointEnv, ""),
		S3PublicEndpoint:    common.GetEnvOrDefaultString(TaskArtifactStoreS3PublicEndpointEnv, ""),
		S3Bucket:            common.GetEnvOrDefaultString(TaskArtifactStoreS3BucketEnv, ""),
		S3Region:            common.GetEnvOrDefaultString(TaskArtifactStoreS3RegionEnv, ""),
		S3AccessKey:         common.GetEnvOrDefaultString(TaskArtifactStoreS3AccessKeyEnv, ""),
		S3SecretKey:         common.GetEnvOrDefaultString(TaskArtifactStoreS3SecretKeyEnv, ""),
		S3Prefix:            common.GetEnvOrDefaultString(TaskArtifactStoreS3PrefixEnv, ""),
		S3PresignTTLSeconds: common.GetEnvOrDefault(TaskArtifactStoreS3PresignTTLEnv, DefaultTaskArtifactStorePresignTTLSeconds),
		S3PathStyle:         common.GetEnvOrDefaultBool(TaskArtifactStoreS3PathStyleEnv, false),
		RetentionDays:       common.GetEnvOrDefault(TaskArtifactStoreRetentionEnv, DefaultTaskArtifactStoreRetentionDays),
		SyncIntervalSeconds: common.GetEnvOrDefault(TaskArtifactStoreSyncIntervalEnv, DefaultTaskArtifactStoreSyncIntervalSecs),
	}
	if err := ValidateTaskArtifactStoreConfig(config); err != nil {
		common.SysError("invalid task artifact store configuration: " + err.Error() + "; using upstream mode")
		config.Mode = TaskArtifactStoreModeUpstream
		return config
	}
	return config
}

// ValidateTaskArtifactStoreConfig performs syntax checks only. It never
// resolves hosts, contacts an endpoint, or verifies credentials.
func ValidateTaskArtifactStoreConfig(config TaskArtifactStoreConfig) error {
	if config.Mode != TaskArtifactStoreModeUpstream && config.Mode != TaskArtifactStoreModeS3 {
		return fmt.Errorf("unsupported mode %q", config.Mode)
	}
	if config.S3PresignTTLSeconds <= 0 || config.S3PresignTTLSeconds > MaxTaskArtifactStorePresignTTLSeconds {
		return fmt.Errorf("S3 presign TTL must be between 1 and %d seconds", MaxTaskArtifactStorePresignTTLSeconds)
	}
	if config.RetentionDays < 1 {
		return errors.New("retention days must be at least 1")
	}
	if config.SyncIntervalSeconds < MinTaskArtifactStoreSyncIntervalSecs {
		return fmt.Errorf("sync interval must be at least %d seconds", MinTaskArtifactStoreSyncIntervalSecs)
	}

	requireS3Fields := config.Mode == TaskArtifactStoreModeS3
	if requireS3Fields && config.S3Endpoint == "" {
		return errors.New("S3 endpoint is required")
	}
	if err := validateTaskArtifactStoreEndpoint("endpoint", config.S3Endpoint); err != nil {
		return err
	}
	if err := validateTaskArtifactStoreEndpoint("public endpoint", config.S3PublicEndpoint); err != nil {
		return err
	}

	if requireS3Fields && config.S3Bucket == "" {
		return errors.New("S3 bucket is required")
	}
	if config.S3Bucket != "" {
		if !taskArtifactStoreBucketPattern.MatchString(config.S3Bucket) ||
			strings.Contains(config.S3Bucket, "..") || net.ParseIP(config.S3Bucket) != nil {
			return errors.New("S3 bucket syntax is invalid")
		}
	}

	if requireS3Fields && config.S3Region == "" {
		return errors.New("S3 region is required")
	}
	if config.S3Region != "" && !taskArtifactStoreRegionPattern.MatchString(config.S3Region) {
		return errors.New("S3 region syntax is invalid")
	}
	if err := validateTaskArtifactStoreCredential("access key", config.S3AccessKey, 256, requireS3Fields); err != nil {
		return err
	}
	if err := validateTaskArtifactStoreCredential("secret key", config.S3SecretKey, 1024, requireS3Fields); err != nil {
		return err
	}

	if config.S3Prefix != "" {
		if config.S3Prefix != strings.TrimSpace(config.S3Prefix) || len(config.S3Prefix) > 512 ||
			strings.HasPrefix(config.S3Prefix, "/") || strings.Contains(config.S3Prefix, "\\") {
			return errors.New("S3 prefix syntax is invalid")
		}
		for part := range strings.SplitSeq(config.S3Prefix, "/") {
			if part == "." || part == ".." {
				return errors.New("S3 prefix must not contain dot segments")
			}
		}
		for _, character := range config.S3Prefix {
			if unicode.IsControl(character) {
				return errors.New("S3 prefix must not contain control characters")
			}
		}
	}
	return nil
}

func validateTaskArtifactStoreEndpoint(name, value string) error {
	if value == "" {
		return nil
	}
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("S3 %s must not contain surrounding whitespace", name)
	}
	endpoint, err := url.Parse(value)
	if err != nil || endpoint == nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Opaque != "" {
		return fmt.Errorf("S3 %s must be an absolute URL without userinfo", name)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return fmt.Errorf("S3 %s must use http or https", name)
	}
	if endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return fmt.Errorf("S3 %s must not contain a query or fragment", name)
	}
	return nil
}

func validateTaskArtifactStoreCredential(name, value string, maxLength int, required bool) error {
	if required && value == "" {
		return fmt.Errorf("S3 %s is required", name)
	}
	if value == "" {
		return nil
	}
	if value != strings.TrimSpace(value) || len(value) > maxLength {
		return fmt.Errorf("S3 %s syntax is invalid", name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("S3 %s syntax is invalid", name)
		}
	}
	return nil
}
