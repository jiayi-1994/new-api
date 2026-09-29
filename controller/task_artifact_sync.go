package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaychannel "github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
)

const (
	taskArtifactSyncBatchSize    = 200
	taskArtifactSyncFetchTimeout = 10 * time.Minute
)

type taskArtifactSyncSummary struct {
	Scanned int `json:"scanned"`
	Stored  int `json:"stored"`
	Failed  int `json:"failed"`
}

// runTaskArtifactSyncOnce walks successful tasks inside the retention window
// and copies each video result into the artifact store once. Failures are
// counted on the task and retried on later runs up to
// model.MaxTaskArtifactStoreAttempts.
func runTaskArtifactSyncOnce(ctx context.Context) taskArtifactSyncSummary {
	store := service.GetTaskArtifactStore()
	config := service.GetTaskArtifactStoreConfig()
	cutoff := time.Now().Add(-time.Duration(config.RetentionDays) * 24 * time.Hour).Unix()
	summary := taskArtifactSyncSummary{}
	// ponytail: full-window scan each run, skipping stored/abandoned rows in Go;
	// add a stored flag column + index once monthly successes pass ~100k.
	var lastID int64
	for ctx.Err() == nil {
		tasks, err := model.GetSuccessTasksFinishedAfter(cutoff, lastID, taskArtifactSyncBatchSize)
		if err != nil {
			common.SysLog(fmt.Sprintf("task artifact sync query failed: %v", err))
			break
		}
		if len(tasks) == 0 {
			break
		}
		lastID = tasks[len(tasks)-1].ID
		for _, task := range tasks {
			if ctx.Err() != nil {
				break
			}
			summary.Scanned++
			if task.PrivateData.StoredArtifact != nil ||
				task.PrivateData.StoreAttempts >= model.MaxTaskArtifactStoreAttempts ||
				!task.ResultRetrievable() {
				continue
			}
			ref, err := storeTaskVideo(ctx, store, task)
			if err != nil {
				task.PrivateData.StoreAttempts++
				summary.Failed++
				logger.LogWarn(ctx, fmt.Sprintf("task %s artifact store copy failed (attempt %d): %v",
					task.TaskID, task.PrivateData.StoreAttempts, err))
			} else {
				task.PrivateData.StoredArtifact = ref
				summary.Stored++
			}
			if updateErr := task.UpdatePrivateData(); updateErr != nil {
				common.SysLog(fmt.Sprintf("task %s artifact store state save failed: %v", task.TaskID, updateErr))
			}
		}
	}
	return summary
}

func storeTaskVideo(ctx context.Context, store service.TaskArtifactStore, task *model.Task) (*service.StoredArtifactRef, error) {
	ctx, cancel := context.WithTimeout(ctx, taskArtifactSyncFetchTimeout)
	defer cancel()

	artifactKey, descriptor := resolveVideoContentRequest(ctx, task, http.MethodGet, nil)
	rawURL := strings.TrimSpace(descriptor.URL)
	if rawURL == "" {
		return nil, errors.New("no result url")
	}
	artifact := types.TaskArtifact{Key: artifactKey, Type: "video", MimeType: "video/mp4"}
	if strings.HasPrefix(rawURL, "data:") {
		mimeType, _, body, err := decodeVideoDataURL(rawURL)
		if err != nil {
			return nil, err
		}
		artifact.MimeType = mimeType
		return store.Persist(ctx, task, artifact, body)
	}
	if isTaskMediaFallbackLoop(rawURL, task.TaskID) {
		return nil, errors.New("result url points back to this server")
	}
	body, contentType, err := fetchTaskMediaForStore(ctx, task, descriptor)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	if mediaType, _, _ := strings.Cut(contentType, ";"); strings.HasPrefix(strings.TrimSpace(mediaType), "video/") {
		artifact.MimeType = strings.TrimSpace(mediaType)
	}
	return store.Persist(ctx, task, artifact, body)
}

// fetchTaskMediaForStore downloads the artifact bytes the way proxyTaskMedia
// would forward them, but follows storage redirects to the end because the
// bytes are needed here rather than by the client.
func fetchTaskMediaForStore(ctx context.Context, task *model.Task, descriptor *relaychannel.TaskContentRequest) (io.ReadCloser, string, error) {
	rawURL := strings.TrimSpace(descriptor.URL)
	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL == nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") ||
		parsedURL.Host == "" || parsedURL.User != nil {
		return nil, "", errTaskMediaRequestRejected
	}
	proxy := ""
	if channel, channelErr := model.CacheGetChannel(task.ChannelId); channelErr == nil {
		proxy = strings.TrimSpace(channel.GetSetting().Proxy)
	}
	if err := validateTaskMediaURL(rawURL, proxy); err != nil {
		return nil, "", err
	}
	client := service.GetSSRFProtectedHTTPClient()
	if proxy != "" {
		client, err = service.GetHttpClientWithProxy(proxy)
		if err != nil {
			return nil, "", err
		}
	}
	if client == nil {
		client = http.DefaultClient
	}

	method := strings.ToUpper(strings.TrimSpace(descriptor.Method))
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, parsedURL.String(), bytes.NewReader(descriptor.Body))
	if err != nil {
		return nil, "", err
	}
	if err := applyTaskMediaRequestHeaders(req.Header, descriptor.Headers); err != nil {
		return nil, "", err
	}

	fetcher := *client
	fetcher.Timeout = 0 // bounded by ctx
	fetcher.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("%w: too many redirects", errTaskMediaRequestRejected)
		}
		if req.URL == nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.Host == "" || req.URL.User != nil {
			return fmt.Errorf("%w: invalid redirect URL", errTaskMediaRequestRejected)
		}
		if err := validateTaskMediaURL(req.URL.String(), proxy); err != nil {
			return fmt.Errorf("%w: %v", errTaskMediaRequestRejected, err)
		}
		if !sameTaskMediaOrigin(via[len(via)-1].URL, req.URL) {
			for name := range req.Header {
				req.Header.Del(name)
			}
		}
		return nil
	}
	resp, err := fetcher.Do(req)
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, "", fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}
	return resp.Body, resp.Header.Get("Content-Type"), nil
}
