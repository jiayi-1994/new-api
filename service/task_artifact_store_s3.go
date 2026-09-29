package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gin-gonic/gin"
)

// s3ArtifactStore keeps task artifacts in an S3-compatible bucket (AWS, Aliyun
// OSS, Cloudflare R2, MinIO) and serves them through short-lived presigned
// GET URLs. Objects are expected to be expired by a bucket lifecycle rule
// matching RetentionDays; the store only stops resolving them after that.
type s3ArtifactStore struct {
	client     *s3.Client
	presigner  *s3.PresignClient
	bucket     string
	prefix     string
	presignTTL time.Duration
	retention  time.Duration
}

func newS3ArtifactStore(config system_setting.TaskArtifactStoreConfig) *s3ArtifactStore {
	client := s3.New(s3.Options{
		Region:       config.S3Region,
		BaseEndpoint: aws.String(config.S3Endpoint),
		UsePathStyle: config.S3PathStyle,
		Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(config.S3AccessKey, config.S3SecretKey, "")),
		// Third-party S3 implementations reject the aws-chunked trailer
		// checksums the SDK adds by default.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	})
	return &s3ArtifactStore{
		client:     client,
		presigner:  s3.NewPresignClient(client),
		bucket:     config.S3Bucket,
		prefix:     strings.Trim(config.S3Prefix, "/"),
		presignTTL: time.Duration(config.S3PresignTTLSeconds) * time.Second,
		retention:  time.Duration(config.RetentionDays) * 24 * time.Hour,
	}
}

func (s *s3ArtifactStore) Enabled() bool {
	return true
}

func (s *s3ArtifactStore) Resolve(task *model.Task, artifactKey string) (*StoredArtifactRef, error) {
	if task == nil {
		return nil, nil
	}
	stored := task.PrivateData.StoredArtifact
	if stored == nil || stored.ArtifactKey != artifactKey || stored.ObjectKey == "" {
		return nil, nil
	}
	if task.FinishTime > 0 && time.Since(time.Unix(task.FinishTime, 0)) > s.retention {
		return nil, nil
	}
	return stored, nil
}

func (s *s3ArtifactStore) Persist(ctx context.Context, task *model.Task, artifact types.TaskArtifact, content io.Reader) (*StoredArtifactRef, error) {
	if task == nil || strings.TrimSpace(task.TaskID) == "" || strings.TrimSpace(artifact.Key) == "" {
		return nil, errors.New("task artifact store: task id and artifact key are required")
	}
	// PutObject needs Content-Length up front (OSS rejects chunked uploads),
	// so spool the body to a temp file first.
	spool, err := os.CreateTemp("", "task-artifact-*")
	if err != nil {
		return nil, fmt.Errorf("task artifact store: spool: %w", err)
	}
	defer func() {
		_ = spool.Close()
		_ = os.Remove(spool.Name())
	}()
	size, err := io.Copy(spool, content)
	if err != nil {
		return nil, fmt.Errorf("task artifact store: read content: %w", err)
	}
	if size == 0 {
		return nil, errors.New("task artifact store: empty content")
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("task artifact store: rewind: %w", err)
	}

	mimeType := strings.TrimSpace(artifact.MimeType)
	if mimeType == "" {
		mimeType = "video/mp4"
	}
	objectKey := path.Join(s.prefix, task.TaskID, artifact.Key+artifactExtension(mimeType))
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(objectKey),
		Body:          spool,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(mimeType),
	})
	if err != nil {
		return nil, fmt.Errorf("task artifact store: put object: %w", err)
	}
	return &StoredArtifactRef{
		ArtifactKey: artifact.Key,
		Bucket:      s.bucket,
		ObjectKey:   objectKey,
		MimeType:    mimeType,
		Size:        size,
		StoredAt:    time.Now().Unix(),
	}, nil
}

func (s *s3ArtifactStore) Serve(c *gin.Context, task *model.Task, ref *StoredArtifactRef) error {
	if ref == nil || ref.ObjectKey == "" {
		return errors.New("task artifact store: empty reference")
	}
	bucket := ref.Bucket
	if bucket == "" {
		bucket = s.bucket
	}
	presigned, err := s.presigner.PresignGetObject(c.Request.Context(), &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(ref.ObjectKey),
	}, s3.WithPresignExpires(s.presignTTL))
	if err != nil {
		return fmt.Errorf("task artifact store: presign: %w", err)
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Redirect(http.StatusFound, presigned.URL)
	return nil
}

func artifactExtension(mimeType string) string {
	switch strings.ToLower(mimeType) {
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "audio/mpeg":
		return ".mp3"
	default:
		return ".mp4"
	}
}
