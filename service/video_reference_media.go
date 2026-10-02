package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/pkg/videosched"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/abema/go-mp4"
	"github.com/gin-gonic/gin"
)

const videoReferenceMetadataKey = "video_reference_metadata"

// All candidates and retries share a request-scoped budget and cache. No media
// URL (which may carry a signature) is written to the task or scheduling audit.
type videoReferenceMetadata struct {
	deadline time.Time
	bytes    int64
	requests int
	results  map[string]videoReferenceDuration
}

type videoReferenceDuration struct {
	seconds float64
	err     error
}

func resolveVideoInputSeconds(c *gin.Context, spec *videosched.Spec, cost videosched.CostConfig) error {
	if spec.References["video"] == 0 {
		return nil
	}
	rules := cost.References["video"]
	rule, found := rules[spec.Tier]
	if !found || spec.Tier == "" {
		rule = rules["*"]
	}
	if rule.Mode != videosched.RefPerInputSecond {
		return nil
	}
	spec.InputVideoSeconds = nil
	if len(spec.ReferenceVideoURLs) != spec.References["video"] {
		return errors.New("plugin does not expose every input video URL")
	}
	cache, _ := c.Value(videoReferenceMetadataKey).(*videoReferenceMetadata)
	if cache == nil {
		cache = &videoReferenceMetadata{
			deadline: time.Now().Add(10 * time.Second), bytes: 8 << 20, requests: 256,
			results: make(map[string]videoReferenceDuration),
		}
		c.Set(videoReferenceMetadataKey, cache)
	}
	ctx, cancel := context.WithDeadline(c.Request.Context(), cache.deadline)
	defer cancel()
	total := 0.0
	for i, address := range spec.ReferenceVideoURLs {
		result, ok := cache.results[address]
		if !ok {
			result.seconds, result.err = readReferenceVideoDuration(ctx, address, cache)
			cache.results[address] = result
		}
		if result.err != nil {
			return fmt.Errorf("reference video %d: %w", i+1, result.err)
		}
		total += result.seconds
		if !(total > 0) || total > relaycommon.MaxTaskDurationSeconds || math.IsInf(total, 0) {
			return errors.New("total input video duration exceeds the supported bound")
		}
	}
	spec.InputVideoSeconds = &total
	return nil
}

// videoMetadataReader fetches small ranges, so a moov box after a large mdat
// does not require downloading the video. Origins ignoring Range are accepted
// only for small files. The protected client checks redirects and resolved IPs;
// no relay credentials, cookies or client headers are forwarded to media URLs.
type videoMetadataReader struct {
	ctx                 context.Context
	address             string
	budget              *videoReferenceMetadata
	position, size      int64
	start               int64
	data                []byte
	validator, modified string
}

func (r *videoMetadataReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, errors.New("metadata read cancelled or timed out")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.position >= r.size {
		return 0, io.EOF
	}
	if r.position < r.start || r.position >= r.start+int64(len(r.data)) {
		if err := r.fetch(); err != nil {
			return 0, err
		}
	}
	n := copy(p, r.data[r.position-r.start:])
	r.position += int64(n)
	return n, nil
}

func (r *videoMetadataReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += r.position
	case io.SeekEnd:
		offset += r.size
	default:
		return 0, errors.New("invalid metadata seek")
	}
	if offset < 0 || offset > r.size {
		return 0, errors.New("invalid media box offset")
	}
	r.position = offset
	return offset, nil
}

func (r *videoMetadataReader) fetch() error {
	if r.budget.requests <= 0 || r.budget.bytes <= 0 {
		return errors.New("metadata request budget exceeded")
	}
	r.budget.requests--
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.address, nil)
	if err != nil {
		return errors.New("invalid media URL")
	}
	end := r.position + (32 << 10) - 1
	if r.size > 0 {
		end = min(end, r.size-1)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", r.position, end))
	req.Header.Set("Accept-Encoding", "identity")
	if r.validator != "" {
		req.Header.Set("If-Range", r.validator)
	} else if r.modified != "" {
		req.Header.Set("If-Range", r.modified)
	}
	resp, err := GetSSRFProtectedHTTPClient().Do(req)
	if err != nil {
		// A transport error includes the original signed URL. Keep it private.
		return errors.New("media fetch failed or was denied by fetch policy")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("media server returned HTTP %d", resp.StatusCode)
	}
	if (r.validator != "" && r.validator != resp.Header.Get("ETag")) || (r.modified != "" && r.modified != resp.Header.Get("Last-Modified")) {
		return errors.New("media changed during metadata read")
	}
	limit := min(int64(1<<20), r.budget.bytes)
	if resp.StatusCode == http.StatusPartialContent {
		limit = min(end-r.position+1, r.budget.bytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	r.budget.bytes -= int64(len(data))
	if err != nil || int64(len(data)) > limit {
		return errors.New("media metadata response exceeds the read limit or is incomplete")
	}
	start, size := int64(0), int64(len(data))
	if resp.StatusCode == http.StatusPartialContent {
		var last int64
		header := resp.Header.Get("Content-Range")
		n, err := fmt.Sscanf(header, "bytes %d-%d/%d", &start, &last, &size)
		if err != nil || n != 3 || header != fmt.Sprintf("bytes %d-%d/%d", start, last, size) || start != r.position || last < start || last > end || size <= last || last-start+1 != int64(len(data)) {
			return errors.New("invalid media Content-Range")
		}
	} else if r.position != 0 {
		return errors.New("media server does not support byte ranges")
	}
	if size <= 0 || size > 2<<30 || (r.size > 0 && size != r.size) {
		return errors.New("invalid or changing media size")
	}
	r.size, r.start, r.data = size, start, data
	r.validator, r.modified = resp.Header.Get("ETag"), resp.Header.Get("Last-Modified")
	return nil
}

// Read the movie header of MP4/MOV files, skipping media payloads and tracks.
// Unknown/fragmented lengths, malformed headers and excessive layouts remain
// unquotable; they never fall back to the generated video's duration.
func readReferenceVideoDuration(ctx context.Context, address string, budget *videoReferenceMetadata) (float64, error) {
	r := &videoMetadataReader{ctx: ctx, address: address, budget: budget}
	if err := r.fetch(); err != nil {
		return 0, err
	}
	parentEnd := r.size
	inMovie := false
	var duration *float64
	for range 512 {
		if r.position == parentEnd {
			break
		}
		box, err := mp4.ReadBoxInfo(r)
		if err != nil {
			return 0, fmt.Errorf("invalid MP4/MOV metadata: %w", err)
		}
		if box.Size < box.HeaderSize || box.Size > uint64(parentEnd)-box.Offset {
			return 0, errors.New("invalid MP4/MOV metadata")
		}
		if box.Type == mp4.BoxTypeMoov() && !inMovie {
			inMovie, parentEnd = true, int64(box.Offset+box.Size)
			continue
		}
		if inMovie && box.Type == mp4.BoxTypeMvhd() {
			if duration != nil || box.Size > 1024 {
				return 0, errors.New("invalid movie duration header")
			}
			payload, _, err := mp4.UnmarshalAny(r, box.Type, box.Size-box.HeaderSize, box.Context)
			if err != nil {
				return 0, fmt.Errorf("invalid movie duration header: %w", err)
			}
			header, ok := payload.(*mp4.Mvhd)
			if !ok || header.Timescale == 0 {
				return 0, errors.New("invalid movie duration header")
			}
			seconds := float64(header.GetDuration()) / float64(header.Timescale)
			if !(seconds > 0) || seconds > relaycommon.MaxTaskDurationSeconds || math.IsInf(seconds, 0) {
				return 0, errors.New("input video duration is unknown or out of range")
			}
			duration = &seconds
		}
		if _, err := box.SeekToEnd(r); err != nil {
			return 0, errors.New("invalid MP4/MOV metadata offset")
		}
	}
	if duration == nil || r.position != parentEnd {
		return 0, errors.New("MP4/MOV duration metadata not found within the read limit")
	}
	return *duration, nil
}
