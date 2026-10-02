package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/videosched"
	videospec "github.com/QuantumNous/new-api/pkg/videosched/spec"
	"github.com/QuantumNous/new-api/plugins"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/abema/go-mp4"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// A movie header after media payload exercises byte-range metadata reads.
func referenceVideoFixture(t *testing.T, seconds float64, trailingHeader bool) []byte {
	t.Helper()
	var payload bytes.Buffer
	_, err := mp4.Marshal(&payload, &mp4.Mvhd{Timescale: 1000, DurationV0: uint32(seconds * 1000)}, mp4.Context{})
	require.NoError(t, err)
	var movie bytes.Buffer
	require.NoError(t, binary.Write(&movie, binary.BigEndian, uint32(payload.Len()+16)))
	movie.WriteString("moov")
	require.NoError(t, binary.Write(&movie, binary.BigEndian, uint32(payload.Len()+8)))
	movie.WriteString("mvhd")
	movie.Write(payload.Bytes())
	if !trailingHeader {
		return movie.Bytes()
	}
	media := make([]byte, 2<<20)
	binary.BigEndian.PutUint32(media, uint32(len(media)))
	copy(media[4:], "mdat")
	return append(media, movie.Bytes()...)
}

func TestVideoInputDurationPurchaseQuote(t *testing.T) {
	previousFetch := *system_setting.GetFetchSetting()
	previousClient := ssrfProtectedHTTPClient
	t.Cleanup(func() {
		*system_setting.GetFetchSetting() = previousFetch
		ssrfProtectedHTTPClient = previousClient
	})
	*system_setting.GetFetchSetting() = system_setting.FetchSetting{EnableSSRFProtection: true, AllowPrivateIp: true, AllowedPorts: []string{"1-65535"}}
	ssrfProtectedHTTPClient = newProtectedFetchHTTPClientWithProxy(nil, nil, nil, func(*http.Request) (*url.URL, error) { return nil, nil })
	media := map[string][]byte{
		"/eight.mp4":    referenceVideoFixture(t, 8, true),
		"/twelve.mov":   referenceVideoFixture(t, 12, false),
		"/unknown.mp4":  referenceVideoFixture(t, 0, false),
		"/oversize.mp4": referenceVideoFixture(t, relaycommon.MaxTaskDurationSeconds+1, false),
		"/not-video":    []byte("not a media container"),
		"/fraction.mp4": referenceVideoFixture(t, 8.25, false),
	}
	requests := make(map[string]int)
	var requestMu sync.Mutex
	snapshotRequests := func() map[string]int {
		requestMu.Lock()
		defer requestMu.Unlock()
		return maps.Clone(requests)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Empty(t, r.Header.Get("Cookie"))
		assert.NotEmpty(t, r.Header.Get("Range"))
		requestMu.Lock()
		requests[r.URL.Path]++
		requestMu.Unlock()
		if r.URL.Path == "/unavailable" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		http.ServeContent(w, r, "video.mp4", time.Unix(1, 0), bytes.NewReader(media[r.URL.Path]))
	}))
	t.Cleanup(server.Close)
	price := 0.02
	cost := videosched.CostConfig{Mode: videosched.ModePerVideo, Prices: map[string]float64{"*": 1}, References: map[string]map[string]videosched.ReferenceCost{
		"video": {"*": {Mode: videosched.RefPerInputSecond, Value: &price}},
	}}
	output := 5.0
	input := videosched.Spec{OutputSeconds: &output, Tier: "720p", References: map[string]int{"video": 2}, ReferenceVideoURLs: []string{server.URL + "/eight.mp4?signature=secret", server.URL + "/twelve.mov"}}
	ctx := newVideoSchedTestContext(t)
	ctx.Request.Header.Set("Authorization", "Bearer should-not-reach-media")
	ctx.Request.Header.Set("Cookie", "session=should-not-reach-media")
	require.NoError(t, resolveVideoInputSeconds(ctx, &input, cost))
	require.NotNil(t, input.InputVideoSeconds)
	assert.Equal(t, 20.0, *input.InputVideoSeconds)
	quote := videosched.Quote(cost, input)
	require.Empty(t, quote.Reason)
	assert.InDelta(t, 1.4, quote.TotalUSD, 1e-9)
	require.Len(t, quote.References, 1)
	assert.Equal(t, 20.0, *quote.References[0].Quantity)
	assert.Less(t, snapshotRequests()["/eight.mp4"], 4, "the payload is skipped by byte range")
	before := snapshotRequests()
	for _, seconds := range []float64{10, 30} {
		input.OutputSeconds = &seconds
		require.NoError(t, resolveVideoInputSeconds(ctx, &input, cost))
		assert.InDelta(t, 1.4, videosched.Quote(cost, input).TotalUSD, 1e-9, "output duration cannot change an input fee")
	}
	assert.Equal(t, before, snapshotRequests(), "candidates and retries reuse the measured input")
	input.ReferenceVideoURLs[1] = input.ReferenceVideoURLs[0]
	require.NoError(t, resolveVideoInputSeconds(ctx, &input, cost))
	assert.Equal(t, 16.0, *input.InputVideoSeconds, "each submitted input counts, even when its URL repeats")
	assert.Equal(t, before, snapshotRequests(), "a repeated URL is downloaded only once")
	encoded, err := common.Marshal(input)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "signature")
	assert.NotContains(t, string(encoded), server.URL)
	var replay videosched.Spec
	require.NoError(t, common.Unmarshal(encoded, &replay))
	assert.Equal(t, videosched.Quote(cost, input), videosched.Quote(cost, replay), "historical quotes need no media fetch")
	t.Run("fractional seconds are preserved", func(t *testing.T) {
		spec := videosched.Spec{References: map[string]int{"video": 1}, ReferenceVideoURLs: []string{server.URL + "/fraction.mp4"}}
		require.NoError(t, resolveVideoInputSeconds(newVideoSchedTestContext(t), &spec, cost))
		assert.Equal(t, 8.25, *spec.InputVideoSeconds)
		assert.InDelta(t, 1.165, videosched.Quote(cost, spec).TotalUSD, 1e-9)
	})
	for _, tc := range []struct{ requested, configured string }{{"2160p", "4k"}, {"4k", "2160p"}} {
		t.Run("input reference tier "+tc.requested+" matches "+tc.configured, func(t *testing.T) {
			aliasedCost := videosched.CostConfig{Mode: videosched.ModePerVideo, Prices: map[string]float64{tc.configured: 1},
				References: map[string]map[string]videosched.ReferenceCost{"video": {
					tc.configured: {Mode: videosched.RefPerInputSecond, Value: &price}, "*": {Mode: videosched.RefIncluded},
				}}}
			spec := videosched.Spec{OutputSeconds: &output, Tier: tc.requested, References: map[string]int{"video": 1}, ReferenceVideoURLs: []string{server.URL + "/twelve.mov"}}
			before := snapshotRequests()
			require.NoError(t, resolveVideoInputSeconds(newVideoSchedTestContext(t), &spec, aliasedCost))
			require.NotNil(t, spec.InputVideoSeconds)
			assert.Equal(t, 12.0, *spec.InputVideoSeconds)
			assert.Greater(t, snapshotRequests()["/twelve.mov"], before["/twelve.mov"], "matching the tier must read actual input media")
			quote := videosched.Quote(aliasedCost, spec)
			require.Empty(t, quote.Reason)
			assert.InDelta(t, 1.24, quote.TotalUSD, 1e-9)
		})
	}
	t.Run("no input video needs no metadata and no surcharge", func(t *testing.T) {
		spec := videosched.Spec{References: map[string]int{"video": 0}}
		require.NoError(t, resolveVideoInputSeconds(newVideoSchedTestContext(t), &spec, cost))
		assert.Equal(t, 1.0, videosched.Quote(cost, spec).TotalUSD)
		spec.References["video"] = 1
		require.ErrorContains(t, resolveVideoInputSeconds(newVideoSchedTestContext(t), &spec, cost), "every input video URL")
	})
	for _, path := range []string{"/unknown.mp4", "/oversize.mp4", "/not-video", "/unavailable"} {
		t.Run(path, func(t *testing.T) {
			spec := videosched.Spec{OutputSeconds: &output, References: map[string]int{"video": 1}, ReferenceVideoURLs: []string{server.URL + path}}
			require.Error(t, resolveVideoInputSeconds(newVideoSchedTestContext(t), &spec, cost))
			assert.Nil(t, spec.InputVideoSeconds)
			assert.Equal(t, "input video seconds unknown", videosched.Quote(cost, spec).Reason)
		})
	}
	t.Run("private media follows the configured fetch policy", func(t *testing.T) {
		system_setting.GetFetchSetting().AllowPrivateIp = false
		defer func() { system_setting.GetFetchSetting().AllowPrivateIp = true }()
		before := snapshotRequests()
		spec := videosched.Spec{References: map[string]int{"video": 1}, ReferenceVideoURLs: []string{server.URL + "/twelve.mov?signature=secret"}}
		err := resolveVideoInputSeconds(newVideoSchedTestContext(t), &spec, cost)
		require.ErrorContains(t, err, "fetch policy")
		assert.NotContains(t, err.Error(), "signature")
		assert.Equal(t, before, snapshotRequests())
	})
	t.Run("other fee modes never read input media", func(t *testing.T) {
		for _, mode := range []string{videosched.RefIncluded, videosched.RefPerInput, videosched.RefPerOutputSecond} {
			other := cost
			other.References = map[string]map[string]videosched.ReferenceCost{"video": {"*": {Mode: mode, Value: &price}}}
			spec := videosched.Spec{OutputSeconds: &output, References: map[string]int{"video": 1}, ReferenceVideoURLs: []string{server.URL + "/unavailable"}}
			before := snapshotRequests()
			require.NoError(t, resolveVideoInputSeconds(newVideoSchedTestContext(t), &spec, other))
			assert.Equal(t, before, snapshotRequests())
		}
	})
	t.Run("untrusted quantities and unsupported reference kinds cannot quote", func(t *testing.T) {
		for _, seconds := range []float64{-1, 0, math.NaN(), math.Inf(1), relaycommon.MaxTaskDurationSeconds + 1} {
			spec := videosched.Spec{InputVideoSeconds: &seconds, References: map[string]int{"video": 1}}
			assert.Equal(t, "invalid input video seconds", videosched.Quote(cost, spec).Reason)
		}
		saved := dto.VideoSchedulingConfig{Models: map[string]dto.VideoModelCost{"video": {Mode: dto.VideoCostPerVideo, Prices: map[string]float64{"*": 1}, References: map[string]map[string]dto.VideoReferenceCost{"image": {"*": {Mode: dto.VideoRefPerInputSecond, Value: &price}}}}}}
		require.ErrorContains(t, saved.Validate(100, relaycommon.MaxTaskDurationSeconds), "requires video")
		saved.Models["video"].References["video"] = saved.Models["video"].References["image"]
		delete(saved.Models["video"].References, "image")
		require.NoError(t, saved.Validate(100, relaycommon.MaxTaskDurationSeconds))
	})
}

func TestVideoInputMetadataReadLimits(t *testing.T) {
	small, tail := referenceVideoFixture(t, 8, false), referenceVideoFixture(t, 8, true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/small-full":
			_, _ = w.Write(small)
		case "/large-full":
			_, _ = w.Write(tail)
		case "/bad-range":
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 1-%d/%d", len(small), len(small)+1))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(small)
		case "/truncated":
			w.Header().Set("Content-Range", "bytes 0-999/1000")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(small)
		case "/too-large":
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/2147483649", len(small)-1))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(small)
		case "/changing":
			w.Header().Set("ETag", `"first"`)
			if r.Header.Get("Range") != "bytes=0-32767" {
				w.Header().Set("ETag", `"changed"`)
			}
			http.ServeContent(w, r, "video.mp4", time.Unix(1, 0), bytes.NewReader(tail))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	previousClient, previousFetch := ssrfProtectedHTTPClient, *system_setting.GetFetchSetting()
	t.Cleanup(func() {
		ssrfProtectedHTTPClient = previousClient
		*system_setting.GetFetchSetting() = previousFetch
	})
	system_setting.GetFetchSetting().EnableSSRFProtection = true
	ssrfProtectedHTTPClient = server.Client()
	for _, tc := range []struct{ path, reason string }{
		{"/small-full", ""},
		{"/large-full", "read limit"},
		{"/bad-range", "Content-Range"},
		{"/truncated", "Content-Range"},
		{"/too-large", "media size"},
		{"/changing", "media changed"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			seconds, err := readReferenceVideoDuration(t.Context(), server.URL+tc.path, &videoReferenceMetadata{bytes: 8 << 20, requests: 256})
			if tc.reason != "" {
				require.ErrorContains(t, err, tc.reason)
				assert.Zero(t, seconds)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 8.0, seconds)
		})
	}
	for _, budget := range []videoReferenceMetadata{{bytes: 1, requests: 1}, {bytes: 1000, requests: 0}, {bytes: 0, requests: 1}} {
		_, err := readReferenceVideoDuration(t.Context(), server.URL+"/small-full", &budget)
		require.Error(t, err, "the shared request budget cannot be exceeded")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := readReferenceVideoDuration(ctx, server.URL+"/small-full", &videoReferenceMetadata{bytes: 8 << 20, requests: 256})
	require.Error(t, err)
}

func TestVideoInputDurationDatabaseRoundTrip(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := useVideoReliabilityDatabase(t, dialect)
			var version string
			if dialect == "sqlite" {
				require.NoError(t, db.Raw("SELECT sqlite_version()").Scan(&version).Error)
			} else {
				require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
			}
			t.Logf("%s: %s", dialect, version)
			auditTables := []any{&model.VideoScheduleRun{}, &model.VideoScheduleDecision{}, &model.Ability{}}
			require.NoError(t, db.Migrator().DropTable(auditTables...))
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(auditTables...)) })
			require.NoError(t, db.AutoMigrate(auditTables...))
			createVideoSchedChannel(t, db, 9811, "default", "seedance-hjmie", 0, `{"video_scheduling":{"models":{"videos-fast":{"mode":"per_video","prices":{"*":1},"references":{"video":{"*":{"mode":"per_input_second","value":0.02}}}}}}}`, "")
			var channel model.Channel
			require.NoError(t, db.First(&channel, 9811).Error)
			config, ok := VideoSchedulingConfigOf(&channel)
			require.True(t, ok)
			require.NoError(t, config.Validate(100, relaycommon.MaxTaskDurationSeconds))
			output, input := 5.0, 20.0
			candidate := videosched.Candidate{ID: 9811, Cost: videoCostConfig(config.Models["videos-fast"]), Spec: videosched.Spec{OutputSeconds: &output, InputVideoSeconds: &input, References: map[string]int{"video": 2}, ReferenceVideoURLs: []string{"https://media.example/private?token=secret"}}}
			quote := videosched.Quote(candidate.Cost, candidate.Spec)
			require.Empty(t, quote.Reason)
			row := VideoScheduleBoard([]videosched.Score{{Candidate: &candidate, Quote: quote}})[0]
			for i, spec := range []*model.VideoSpecView{{OutputSeconds: &output, References: map[string]int{"video": 1}}, row.Spec} {
				task := model.Task{TaskID: fmt.Sprintf("input-duration-%d", i), Status: model.TaskStatusSuccess, PrivateData: model.TaskPrivateData{SchedulingSummary: &model.TaskSchedulingSummary{Spec: spec}}}
				require.NoError(t, db.Create(&task).Error)
				var restored model.Task
				require.NoError(t, db.First(&restored, task.ID).Error)
				assert.Equal(t, spec, restored.PrivateData.SchedulingSummary.Spec, "legacy absent and new measured durations remain distinct")
			}
			inputJSON, err := common.Marshal(VideoDecisionInput{Candidates: []videosched.Candidate{candidate}})
			require.NoError(t, err)
			boardJSON, err := common.Marshal([]VideoScheduleRow{row})
			require.NoError(t, err)
			audit := model.VideoScheduleAudit{Run: model.VideoScheduleRun{RequestID: "input-duration", StartedAt: time.Now().UnixMilli()}, Decisions: []model.VideoScheduleDecision{{SelectionSeq: 1, InputJSON: model.VideoAuditSnapshot(inputJSON), BoardJSON: model.VideoAuditSnapshot(boardJSON)}}}
			require.NoError(t, model.InsertVideoScheduleAudit(t.Context(), &audit))
			stored, err := model.GetVideoScheduleAudit(t.Context(), "input-duration")
			require.NoError(t, err)
			require.Len(t, stored.Decisions, 1)
			assert.NotContains(t, string(stored.Decisions[0].InputJSON), "secret")
			var replay VideoDecisionInput
			require.NoError(t, common.UnmarshalJsonStr(string(stored.Decisions[0].InputJSON), &replay))
			require.Len(t, replay.Candidates, 1)
			assert.Equal(t, quote, videosched.Quote(replay.Candidates[0].Cost, replay.Candidates[0].Spec))
		})
	}
}

func TestVideoInputSourceSpecVersion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		version   int64
		count     int64
		urls      any
		wantError string
	}{
		{"complete sources", 2, 2, []any{"https://media.example/a.mp4", "https://media.example/b.mp4"}, ""},
		{"version one cannot reinterpret a source field", 1, 1, []any{"https://media.example/a.mp4"}, "requires spec_version 2"},
		{"partial sources", 2, 2, []any{"https://media.example/a.mp4"}, "every submitted"},
		{"not an array", 2, 1, "https://media.example/a.mp4", "every submitted"},
		{"non HTTP source", 2, 1, []any{"file:///video.mp4"}, "HTTP(S)"},
		{"embedded credentials", 2, 1, []any{"https://user:secret@media.example/a.mp4"}, "without credentials"},
		{"future version", 3, 0, []any{}, "version unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{"spec_version": tc.version, "references": map[string]any{"video": tc.count, "image": int64(0), "audio": int64(0)}, "reference_video_urls": tc.urls}
			spec, _, err := videospec.Parse(raw)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			assert.Len(t, spec.ReferenceVideoURLs, int(tc.count))
			assert.Nil(t, spec.InputVideoSeconds, "only the host's measured metadata can set duration")
		})
	}
}

func TestReliableVideoColdStartAndValidationOrder(t *testing.T) {
	now := time.Unix(1800000000, 0).UTC()
	p := videosched.Policy{SelectionPolicy: videosched.PolicyStabilityCostV2, MinMarginRate: .1, MinGenRate: .8, MinOverallRate: .6, StabilityTolerance: .01, MinSamples: 20, Now: now.Unix(), MaxCostUSD: 1000, QualificationTTLSeconds: 86400, ValidationPeriodSeconds: 604800}
	fresh := func(id int) videosched.Candidate {
		return videosched.Candidate{ID: id, Priority: 1, Quality: .8, Sell: videosched.SellPrice{Kind: videosched.SellKnown, USD: 1}, Spec: videosched.Spec{Tier: "*"}, Cost: videosched.CostConfig{Mode: videosched.ModePerVideo, Prices: map[string]float64{"*": .5}}, Reliability: &videosched.ReliabilitySnapshot{Version: 1, Model: "video", State: videosched.HealthUnverified, StateVersion: 1, Integrity: "complete", Reason: "new_channel"}}
	}
	qualified := fresh(3)
	qualified.Reliability.State = videosched.HealthNormal
	qualified.Reliability.Qualification = &videosched.ReliabilityEvidence{Version: 1, Source: "window", BatchStart: now.Unix() - 3600, BatchEnd: now.Unix() - 1800, WindowSeconds: 1800, AsOf: now.Unix() - 900, ValidatedAt: now.Unix() - 900, ExpiresAt: now.Unix() + 85500, Submitted: 20, Accepted: 20, Succeeded: 20}
	t.Run("larger_sample_policy_still_admits_revalidation", func(t *testing.T) {
		changedPolicy := p
		changedPolicy.MinSamples = 40
		input := VideoDecisionInput{Candidates: []videosched.Candidate{qualified}, Policy: changedPolicy, Explore: VideoExploreSettings{ExploreMaxInFlight: 2}, Now: now}
		choice := DecideVideoSchedule(input)
		require.NotNil(t, choice.Best)
		assert.Equal(t, qualified.ID, choice.Best.ID)
		assert.Equal(t, "revalidate", choice.Flow)
		assert.Equal(t, "no_normal_candidate", choice.Reason)
	})
	for _, tc := range []struct {
		name         string
		change       func(*VideoDecisionInput)
		want         int
		flow, reason string
	}{
		{"two_new_channels", func(in *VideoDecisionInput) { in.Candidates = in.Candidates[:2] }, 1, "explore", "no_normal_candidate"},
		{"three_new_channels_zero_probabilities", func(in *VideoDecisionInput) {}, 1, "explore", "no_normal_candidate"},
		{"single_channel_zero_probabilities", func(in *VideoDecisionInput) { in.Candidates = in.Candidates[:1] }, 1, "explore", "no_normal_candidate"},
		{"oldest_validation_first", func(in *VideoDecisionInput) { in.Candidates[0].Reliability.LastValidationAt = now.Unix() - 50 }, 2, "explore", "no_normal_candidate"},
		{"top_tier_full_can_descend", func(in *VideoDecisionInput) { in.Candidates[0].Priority = 2; in.SlotOccupancy[1] = 2 }, 2, "explore", "no_normal_candidate"},
		{"all_validation_slots_full", func(in *VideoDecisionInput) { in.SlotOccupancy = map[int]int{1: 2, 2: 2, 3: 2} }, 0, "", "validation_slots_full"},
		{"mixed_normal_zero_validation_budget", func(in *VideoDecisionInput) { in.Candidates[2] = qualified }, 3, "normal", ""},
		{"mixed_normal_full_validation_budget", func(in *VideoDecisionInput) { in.Candidates[2] = qualified; in.Explore.ExploreShare = 1 }, 1, "explore", "validation_budget"},
		{"normal_tier_does_not_validate_other_priorities", func(in *VideoDecisionInput) {
			in.Candidates[2] = qualified
			in.Candidates[0].Priority, in.Candidates[1].Priority = 2, 0
			in.Explore.ExploreShare = 1
		}, 3, "normal", ""},
		{"blocked_is_not_new", func(in *VideoDecisionInput) {
			for i := range in.Candidates {
				in.Candidates[i].Reliability.State = videosched.HealthBlocked
				in.Candidates[i].Reliability.BlockedAt = now.Unix()
			}
		}, 0, "", "recovery_cooldown"},
		{"ready_recovery_ignores_zero_ratio", func(in *VideoDecisionInput) {
			for i := range in.Candidates {
				in.Candidates[i].Reliability.State = videosched.HealthBlocked
				in.Candidates[i].Reliability.BlockedAt = now.Unix() - 600
			}
		}, 1, "recover", "no_normal_candidate"},
		{"recovery_explicitly_disabled", func(in *VideoDecisionInput) {
			in.Explore.ProbeMaxInFlight = 0
			for i := range in.Candidates {
				in.Candidates[i].Reliability.State = videosched.HealthBlocked
			}
		}, 0, "", "recovery_disabled"},
		{"nonfault_before_recovery", func(in *VideoDecisionInput) {
			in.Candidates[0].Reliability.State = videosched.HealthBlocked
			in.Candidates[0].Reliability.BlockedAt = now.Unix() - 600
		}, 2, "explore", "no_normal_candidate"},
		{"no_history_is_not_new", func(in *VideoDecisionInput) {
			for i := range in.Candidates {
				in.Candidates[i].Reliability = nil
			}
		}, 0, "", "health_state_unavailable"},
		{"margin_still_required", func(in *VideoDecisionInput) {
			for i := range in.Candidates {
				in.Candidates[i].Cost.Prices["*"] = .9001
			}
		}, 0, "", "hard_filter_exhausted"},
		{"all_evidence_expired", func(in *VideoDecisionInput) {
			for i := range in.Candidates {
				h := in.Candidates[i].Reliability
				h.State = videosched.HealthNormal
				h.Qualification = &videosched.ReliabilityEvidence{Version: 1, Source: "window", BatchStart: now.Unix() - 90000, BatchEnd: now.Unix() - 88200, WindowSeconds: 1800, AsOf: now.Unix() - 88000, ValidatedAt: now.Unix() - 88000, ExpiresAt: now.Unix() - 1600, Submitted: 20, Accepted: 20, Succeeded: 20}
			}
		}, 1, "revalidate", "no_normal_candidate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := VideoDecisionInput{Candidates: []videosched.Candidate{fresh(1), fresh(2), fresh(3)}, Policy: p, Explore: VideoExploreSettings{ExploreMaxInFlight: 2, ProbeMaxInFlight: 1, ProbeCooldownSec: 300}, SlotOccupancy: map[int]int{}, Probe: map[int]VideoProbeState{}, Now: now, Seed: 7}
			tc.change(&in)
			result := DecideVideoSchedule(in)
			if tc.want == 0 {
				assert.Nil(t, result.Best)
			} else {
				require.NotNil(t, result.Best)
				assert.Equal(t, tc.want, result.Best.ID)
			}
			assert.Equal(t, tc.flow, result.Flow)
			assert.Equal(t, tc.reason, result.Reason)
		})
	}
}

func TestReliableVideoAdmissionReselectsStaleState(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	useVideoHealthBackend(t, "memory")
	t.Cleanup(func() { videoReliabilityCache.Clear() })
	require.NoError(t, db.AutoMigrate(&model.VideoHealthRegistration{}, &model.VideoHealthState{}, &model.VideoHealthAttempt{}, &model.VideoHealthRequest{}, &model.Task{}))
	s := operation_setting.GetVideoSchedulingSetting()
	s.Mode, s.SelectionPolicy = "on", videosched.PolicyStabilityCostV2
	s.MinSamples, s.MinGenRate, s.MinOverallRate, s.MinMarginRate = 20, .8, .6, .1
	s.QualificationTTLSeconds, s.ValidationPeriodSeconds = 86400, 604800
	s.ExploreMaxInFlight, s.ExploreShare, s.ProbeRatio = 1, 0, 0
	savedPrices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedPrices)) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"videos-fast":2}`))
	plugin, err := jsplugin.NewRegistry().Register(videoSpecProbePlugin, jsplugin.Options{})
	require.NoError(t, err)
	for _, id := range []int{3311, 3312} {
		createVideoSchedChannel(t, db, id, "default", "spec-probe", 1, `{"video_scheduling":{"quality":0.8,"models":{"videos-fast":{"mode":"per_video","prices":{"*":1}}}}}`, "")
		channel, err := model.GetChannelById(id, true)
		require.NoError(t, err)
		require.NoError(t, model.EnsureVideoHealthState(t.Context(), id, "videos-fast", channel.VideoHealthIdentity()))
		publishPersistedVideoReliability(t.Context(), id, "videos-fast")
	}
	model.InitChannelCache()
	// A competing node changes the first choice after its cached snapshot.
	// Admission must release that reservation and choose the other channel.
	require.NoError(t, db.Model(&model.VideoHealthState{}).Where("channel_id = ?", 3311).Update("version", 2).Error)
	c := newVideoSchedTestContext(t)
	c.Set(common.RequestIdKey, "v2-admission-reselection")
	c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Plugin: plugin})
	c.Set("task_request", map[string]any{"prompt": "fixture"})
	common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, VideoSchedDecision{Takeover: true})
	common.SetContextKey(c, constant.ContextKeyVideoSchedSetting, s)
	t.Cleanup(func() { releaseVideoValidationLease(c) })
	selected, err := model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
	require.NoError(t, err)
	require.NotNil(t, selected)
	assert.Equal(t, 3312, selected.Id)
	records := VideoScheduleRecords(c)
	require.Len(t, records, 2)
	assert.Equal(t, 3311, records[0].Recommended)
	assert.Equal(t, "health_state_unavailable", records[0].Admission)
	assert.Equal(t, "explore", records[1].Flow)
	assert.Equal(t, []int{1, 1}, []int{records[0].AttemptSeq, records[1].AttemptSeq})
	assert.NotEqual(t, records[0].Fingerprint, records[1].Fingerprint)
	held, err := videoHealthStore().held(videoValidationKeys(3311))
	require.NoError(t, err)
	assert.Zero(t, held)
	var attempts int64
	require.NoError(t, db.Model(&model.VideoHealthAttempt{}).Count(&attempts).Error)
	assert.Zero(t, attempts, "selection and failed admission never count as submissions")
	RequestPolicy(c).BeginAttempt(selected, "default")
	BindVideoHealthChannel(c, selected, "videos-fast")
	require.NoError(t, BeginVideoHealthTransmission(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: selected.Id}, OriginModelName: "videos-fast"}))
	t.Cleanup(func() { stopVideoHealthSubmissionOwner(c) })
	facts, err := model.ListVideoHealthAttempts(t.Context(), "v2-admission-reselection")
	require.NoError(t, err)
	require.Len(t, facts, 1)
	assert.Equal(t, 3312, facts[0].ChannelID)
	assert.Equal(t, 1, facts[0].AttemptSeq)
}

func TestVideoScheduleAuditVersionFiltersUseFrozenRequestPolicy(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.VideoScheduleRun{}, &model.VideoScheduleDecision{}))
	previousDB := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previousDB })
	setting := operation_setting.GetVideoSchedulingSetting()
	previousPolicy := setting.SelectionPolicy
	t.Cleanup(func() { setting.SelectionPolicy = previousPolicy })

	for _, tc := range []struct {
		name, policy, wantVersion     string
		withDecision, assemblyFailure bool
	}{
		{"legacy_missing_policy", "", "1", true, false},
		{"legacy_weighted_policy", videosched.PolicyWeightedV1, "1", true, false},
		{"stability_selection", videosched.PolicyStabilityCostV2, videosched.PolicyStabilityCostV2, true, false},
		{"stability_no_candidate", videosched.PolicyStabilityCostV2, videosched.PolicyStabilityCostV2, false, false},
		{"stability_assembly_failure", videosched.PolicyStabilityCostV2, videosched.PolicyStabilityCostV2, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newVideoSchedTestContext(t)
			c.Set(common.RequestIdKey, tc.name)
			decision := VideoSchedDecision{Takeover: true}
			common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
			frozen := *setting
			frozen.SelectionPolicy = tc.policy
			common.SetContextKey(c, constant.ContextKeyVideoSchedSetting, &frozen)
			freezeVideoScheduleAudit(c, decision, true)
			// A concurrent settings change must not relabel the in-flight request.
			setting.SelectionPolicy = videosched.PolicyStabilityCostV2
			if tc.policy == videosched.PolicyStabilityCostV2 {
				setting.SelectionPolicy = videosched.PolicyWeightedV1
			}
			if tc.withDecision {
				input := VideoDecisionInput{Now: time.Now().UTC(), Policy: videosched.Policy{SelectionPolicy: tc.policy}}
				fingerprint, _, err := VideoDecisionFingerprint(input)
				require.NoError(t, err)
				common.SetContextKey(c, constant.ContextKeyVideoSchedBoard, []VideoScheduleRecord{{SelectionSeq: 1, AttemptSeq: 1, Input: &input, Fingerprint: fingerprint}})
			}
			if tc.assemblyFailure {
				recordVideoAuditAssemblyError(c)
			}
			audit, _, err := buildVideoScheduleAudit(c, videoScheduleAuditState(c), false)
			require.NoError(t, err)
			assert.Equal(t, tc.wantVersion, audit.Run.SchedulerVersion)
			require.NoError(t, model.InsertVideoScheduleAudit(t.Context(), audit))
			filter := model.VideoScheduleAuditFilter{Start: audit.Run.StartedAt - 1, End: audit.Run.EndedAt + 1, Version: tc.wantVersion, RequestID: tc.name}
			runs, total, err := model.ListVideoScheduleAudits(t.Context(), filter)
			require.NoError(t, err)
			assert.EqualValues(t, 1, total)
			assert.Len(t, runs, 1)
			stats, err := model.GetVideoScheduleAuditStats(t.Context(), filter)
			require.NoError(t, err)
			assert.EqualValues(t, 1, stats.Total)
			exported, err := model.ExportVideoScheduleAudits(t.Context(), filter, "")
			require.NoError(t, err)
			assert.Contains(t, string(exported), `"type":"request"`)
		})
	}
}

func TestVideoScheduleAuditKeepsShadowTerminalAfterOffAndPreservesLogs(t *testing.T) {
	previousErrorLogs, previousConsumeLogs := constant.ErrorLogEnabled, common.LogConsumeEnabled
	constant.ErrorLogEnabled, common.LogConsumeEnabled = false, false
	t.Cleanup(func() { constant.ErrorLogEnabled, common.LogConsumeEnabled = previousErrorLogs, previousConsumeLogs })
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.VideoScheduleRun{}, &model.VideoScheduleDecision{}))
	previousDB, previousWriter := model.DB, videoAuditQueue
	model.DB, videoAuditQueue = db, newVideoAuditWriter()
	t.Cleanup(func() { model.DB, videoAuditQueue = previousDB, previousWriter })
	setVideoSchedulingForTest(t, "shadow")
	for _, immediate := range []bool{false, true} {
		t.Run(fmt.Sprint("immediate=", immediate), func(t *testing.T) {
			c := newVideoSchedTestContext(t)
			requestID := fmt.Sprint("audit-shadow-", immediate)
			c.Set(common.RequestIdKey, requestID)
			c.Set("resolved_task_model", "video")
			c.Set("channel_id", 7)
			decision := VideoSchedDecision{Shadow: true}
			common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
			freezeVideoScheduleAudit(c, decision, true)
			channel := &model.Channel{Id: 7} // no cost table, hence no SchedulingSummary
			RequestPolicy(c).BeginAttempt(channel, "default")
			captureVideoAuditSubmit(c, 7, nil)
			input := VideoDecisionInput{Now: time.Now().UTC().Truncate(time.Second), Seed: 42}
			fingerprint, _, err := VideoDecisionFingerprint(input)
			require.NoError(t, err)
			record := VideoScheduleRecord{Mode: "shadow", SelectionSeq: 1, AttemptSeq: 1, Selected: 7, Recommended: 9, Input: &input, Fingerprint: fingerprint, Candidates: []VideoScheduleRow{{ID: 7}}}
			before, err := common.Marshal(record)
			require.NoError(t, err)
			common.SetContextKey(c, constant.ContextKeyVideoSchedBoard, []VideoScheduleRecord{record})
			task := &model.Task{ID: 123, TaskID: "task", Status: model.TaskStatusSubmitted, PrivateData: model.TaskPrivateData{Execution: &model.TaskExecutionSnapshot{RequestID: requestID}}}
			if immediate {
				task.Status = model.TaskStatusSuccess
			}
			operation_setting.GetVideoSchedulingSetting().Mode = "off"
			VideoTaskPersisted(c, task)
			EnqueueVideoScheduleAudit(c, false)
			videoAuditQueue.flush(context.Background())
			EnqueueVideoScheduleAudit(c, false)
			if !immediate {
				task.Status = model.TaskStatusSuccess
				ObserveVideoTerminal(task, false)
			}
			got, err := model.GetVideoScheduleAudit(context.Background(), requestID)
			require.NoError(t, err)
			assert.Equal(t, string(model.TaskStatusSuccess), got.Run.TaskStatus)
			assert.Equal(t, 1, got.Run.SubmitAccepted)
			require.Len(t, got.Decisions, 1)
			assert.Equal(t, fingerprint, got.Decisions[0].Fingerprint)
			var frozen VideoDecisionInput
			require.NoError(t, common.UnmarshalJsonStr(string(got.Decisions[0].InputJSON), &frozen))
			storedHash, _, err := VideoDecisionFingerprint(frozen)
			require.NoError(t, err)
			assert.Equal(t, fingerprint, storedHash)
			after, err := common.Marshal(VideoScheduleRecords(c)[0])
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after), "private input never changes legacy log JSON")
		})
	}
	for _, tc := range []struct {
		name, reason, class, health string
		timedOut                    bool
	}{
		{"timeout", "任务超时", "host", "fail", true},
		{"poll escalation", "poll failures", "host", "fail", false},
		{"user failure", "user fixture", "user", "ignored", false},
		{"cancelled", "cancel fixture", "cancelled", "ignored", false},
		{"unknown", "unknown fixture", "unknown", "fail", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requestID := "terminal-" + tc.name
			task := &model.Task{Status: model.TaskStatusFailure, FailReason: tc.reason, PrivateData: model.TaskPrivateData{Execution: &model.TaskExecutionSnapshot{RequestID: requestID}}}
			require.NoError(t, model.InsertVideoScheduleAudit(context.Background(), &model.VideoScheduleAudit{Run: model.VideoScheduleRun{RequestID: requestID, StartedAt: time.Now().Add(-time.Minute).UnixMilli()}}))
			outcome := VideoOutcomeFail
			if tc.health == "ignored" {
				outcome = VideoOutcomeIgnored
			}
			if tc.class == "host" {
				ObserveVideoTerminal(task, true)
			} else {
				ObserveVideoScheduleAuditTerminal(task, tc.class, outcome)
			}
			got, err := model.GetVideoScheduleAudit(context.Background(), requestID)
			require.NoError(t, err)
			assert.Equal(t, tc.class, got.Run.TerminalClass)
			assert.Equal(t, tc.health, got.Run.TerminalHealth)
			assert.Equal(t, tc.timedOut, got.Run.TimedOut)
			ObserveVideoScheduleAuditTerminal(task, "upstream", VideoOutcomeFail)
			again, err := model.GetVideoScheduleAudit(context.Background(), requestID)
			require.NoError(t, err)
			assert.Equal(t, got.Run.TerminalObservedAt, again.Run.TerminalObservedAt)
			assert.Equal(t, tc.class, again.Run.TerminalClass)
		})
	}
}

func TestVideoScheduleAuditDistinguishesUnknownCancelledAndAcceptedUnpersisted(t *testing.T) {
	for _, tc := range []struct {
		name    string
		taskErr *taskdto.TaskError
		cancel  bool
		outcome string
	}{
		{"rejected", &taskdto.TaskError{StatusCode: 502}, false, "rejected"},
		{"local", &taskdto.TaskError{LocalError: true, StatusCode: 400}, false, "local_failure"},
		{"unknown", &taskdto.TaskError{Error: fmt.Errorf("transport: %w", relaycommon.ErrTaskSubmitOutcomeUnknown), StatusCode: 502}, false, "outcome_unknown"},
		{"cancelled", &taskdto.TaskError{StatusCode: 502}, true, "cancelled"},
		{"accepted", nil, false, "persistence_failure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newVideoSchedTestContext(t)
			c.Set(common.RequestIdKey, tc.name)
			decision := VideoSchedDecision{Takeover: true}
			common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
			freezeVideoScheduleAudit(c, decision, true)
			RequestPolicy(c).BeginAttempt(&model.Channel{Id: 1}, "default")
			captureVideoAuditSubmit(c, 1, tc.taskErr)
			if tc.cancel {
				RequestPolicy(c).AddEvent(PolicyEvent{ChannelID: 1, Decision: PolicyDecision{Action: "cancelled"}})
			}
			audit, _, err := buildVideoScheduleAudit(c, videoScheduleAuditState(c), false)
			require.NoError(t, err)
			assert.Equal(t, tc.outcome, audit.Run.RequestOutcome)
			if tc.name == "unknown" {
				assert.Equal(t, 1, audit.Run.SubmitUnknown)
				assert.Zero(t, audit.Run.SubmitRejected)
			}
			if tc.cancel {
				assert.Equal(t, 1, audit.Run.SubmitCancelled)
				assert.Zero(t, audit.Run.SubmitRejected)
			}
		})
	}
	for _, assemblyFailed := range []bool{false, true} {
		c := newVideoSchedTestContext(t)
		c.Set(common.RequestIdKey, "no-task-no-selection")
		decision := VideoSchedDecision{Shadow: true}
		common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
		freezeVideoScheduleAudit(c, decision, true)
		if assemblyFailed {
			recordVideoAuditAssemblyError(c)
		}
		audit, _, err := buildVideoScheduleAudit(c, videoScheduleAuditState(c), false)
		require.NoError(t, err)
		assert.Nil(t, audit.Run.TaskPK)
		assert.Empty(t, audit.Decisions)
		if assemblyFailed {
			assert.Equal(t, "candidate_snapshot_failed", audit.Run.AssemblyError)
			assert.Equal(t, "internal_failure", audit.Run.RequestOutcome)
		} else {
			assert.Equal(t, "missing_decisions", audit.Run.DataIssue)
		}
	}
}

func TestVideoScheduleAuditWriterBoundsRecoveryAndShutdown(t *testing.T) {
	for _, tc := range []struct {
		name        string
		size, count int
	}{{"request cap", 1, videoAuditQueueLimit}, {"byte cap", videoAuditByteLimit, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			writer := newVideoAuditWriter()
			for i := range tc.count {
				require.True(t, writer.enqueue(&model.VideoScheduleAudit{Run: model.VideoScheduleRun{RequestID: fmt.Sprint(i)}}, tc.size))
			}
			assert.False(t, writer.enqueue(&model.VideoScheduleAudit{}, 1))
			assert.EqualValues(t, 1, writer.status.Dropped)
			assert.Equal(t, "queue_full_or_stopped", writer.status.LastIssue)
		})
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "writer.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous; sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.VideoScheduleRun{}, &model.VideoScheduleDecision{}))
	writer := newVideoAuditWriter()
	require.True(t, writer.enqueue(&model.VideoScheduleAudit{Run: model.VideoScheduleRun{RequestID: "write-retry"}}, 100))
	failures := 0
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("audit_transient", func(tx *gorm.DB) {
		if failures < 2 {
			failures++
			tx.AddError(errors.New("injected temporary outage"))
		}
	}))
	writer.flush(context.Background())
	assert.EqualValues(t, 1, writer.status.Written)
	assert.Zero(t, writer.status.Dropped)
	assert.Zero(t, writer.status.Pending)
	require.NoError(t, db.Callback().Create().Remove("audit_transient"))
	for _, id := range []string{"shutdown-a", "shutdown-b"} {
		require.True(t, writer.enqueue(&model.VideoScheduleAudit{Run: model.VideoScheduleRun{RequestID: id}}, 100))
	}
	writer.start()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	writer.stopAndFlush(ctx)
	assert.False(t, writer.status.Running)
	assert.Zero(t, writer.status.Pending)
	assert.EqualValues(t, 3, writer.status.Written)
	var count int64
	require.NoError(t, db.Model(&model.VideoScheduleRun{}).Count(&count).Error)
	assert.EqualValues(t, 3, count)
	// Deadline failures are observable and never propagate into relay behavior.
	writer = newVideoAuditWriter()
	require.True(t, writer.enqueue(&model.VideoScheduleAudit{Run: model.VideoScheduleRun{RequestID: "timed-out"}}, 100))
	expired, expire := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer expire()
	writer.flush(expired)
	assert.EqualValues(t, 1, writer.status.WriteFailures)
	assert.EqualValues(t, 1, writer.status.Dropped)
	assert.Zero(t, writer.status.Pending)
	// A cancelled shutdown also releases queued byte accounting and stops the worker.
	writer = newVideoAuditWriter()
	for i := range 101 {
		require.True(t, writer.enqueue(&model.VideoScheduleAudit{Run: model.VideoScheduleRun{RequestID: fmt.Sprintf("stop-%d", i)}}, 100))
	}
	entered := make(chan struct{}, 1)
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("audit_timeout", func(tx *gorm.DB) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-tx.Statement.Context.Done()
		tx.AddError(tx.Statement.Context.Err())
	}))
	writer.start()
	<-entered
	writer.stopAndFlush(expired)
	<-writer.done
	assert.Zero(t, writer.status.Pending)
	assert.Zero(t, writer.status.PendingBytes)
	assert.Greater(t, writer.status.Dropped, int64(0))
	assert.Greater(t, writer.status.WriteFailures, int64(0))
	require.NoError(t, db.Callback().Create().Remove("audit_timeout"))
	previousWriter := videoAuditQueue
	videoAuditQueue = writer
	t.Cleanup(func() { videoAuditQueue = previousWriter })
	beforeFailures := writer.status.WriteFailures
	MarkVideoScheduleAuditMaintenanceFailure(errors.New("private database error"))
	assert.Equal(t, beforeFailures+1, writer.status.WriteFailures)
	assert.Equal(t, "maintenance_failed", writer.status.LastIssue)
	assert.Positive(t, writer.status.FirstIssueAt)
	// A stopped/full audit writer cannot change the response or swallow the
	// business panic that the distribution defer must pass to outer recovery.
	c := newVideoSchedTestContext(t)
	c.Set(common.RequestIdKey, "business-response")
	decision := VideoSchedDecision{Takeover: true}
	common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
	freezeVideoScheduleAudit(c, decision, true)
	c.Writer.WriteHeader(http.StatusAccepted)
	require.PanicsWithValue(t, "original business panic", func() {
		defer EnqueueVideoScheduleAudit(c, true)
		panic("original business panic")
	})
	assert.Equal(t, http.StatusAccepted, c.Writer.Status())
	assert.Equal(t, "queue_full_or_stopped", writer.status.LastIssue)
}

func TestVideoScheduleAuditSnapshotsKeepDecisionsAndExcludeRequestSecrets(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint("enabled=", enabled), func(t *testing.T) {
			c := newVideoSchedTestContext(t)
			c.Set(common.RequestIdKey, "privacy")
			c.Set("api_key", "sensitive-key-fixture")
			c.Set("request_body", `{"prompt":"sensitive-prompt-fixture","url":"https://sensitive.invalid/media"}`)
			decision := VideoSchedDecision{Takeover: true}
			common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
			freezeVideoScheduleAudit(c, decision, enabled)
			input := videoDecisionTestInput()
			healthy := videosched.HealthStat{Rate: 1, Samples: 20}
			for id := 2; id <= 20; id++ {
				input.Candidates = append(input.Candidates, videoDecisionTestCandidate(id, 0, healthy, healthy))
			}
			input.Policy.TieEpsilon = 0.1
			choice := DecideVideoSchedule(input)
			fingerprint, _, err := VideoDecisionFingerprint(input)
			require.NoError(t, err)
			for range 3 {
				appendVideoScheduleRecord(c, VideoScheduleRecord{Input: &input, Fingerprint: fingerprint, Recommended: choice.Best.ID, Probe: true, Admission: "probe_slot_taken", Mode: "on"}, choice.Board)
			}
			before, err := common.Marshal(VideoScheduleRecords(c))
			require.NoError(t, err)
			afterChoice := DecideVideoSchedule(input)
			assert.Equal(t, choice.Best.ID, afterChoice.Best.ID)
			if !enabled {
				for _, record := range VideoScheduleRecords(c) {
					assert.Nil(t, record.Input)
				}
				return
			}
			audit, bytes, err := buildVideoScheduleAudit(c, videoScheduleAuditState(c), false)
			require.NoError(t, err)
			require.Len(t, audit.Decisions, 3)
			total := 0
			for _, row := range audit.Decisions {
				assert.True(t, row.SnapshotComplete)
				total += len(row.InputJSON) + len(row.BoardJSON)
				var frozen VideoDecisionInput
				require.NoError(t, common.UnmarshalJsonStr(string(row.InputJSON), &frozen))
				actual, _, err := VideoDecisionFingerprint(frozen)
				require.NoError(t, err)
				assert.Equal(t, fingerprint, actual)
			}
			encoded, err := common.Marshal(audit)
			require.NoError(t, err)
			for _, secret := range []string{"sensitive-key-fixture", "sensitive-prompt-fixture", "sensitive.invalid"} {
				assert.NotContains(t, string(encoded), secret)
			}
			t.Logf("20 candidates x 3 selections: snapshot bytes=%d serialized request bytes=%d queue estimate=%d; per-decision cap=%d", total, len(encoded), bytes, videoAuditDetailLimit)
			after, err := common.Marshal(VideoScheduleRecords(c))
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after))
			// A provider-controlled error is reduced to a stable category, then
			// explicitly marked incomplete because its input fingerprint changes.
			input.Candidates[0].Excluded = "spec invalid: sensitive-key-fixture https://sensitive.invalid/media"
			input.Candidates[0].Spec.Tier = "https://sensitive.invalid/input"
			choice = DecideVideoSchedule(input)
			appendVideoScheduleRecord(c, VideoScheduleRecord{Input: &input, Mode: "on"}, choice.Board)
			audit, _, err = buildVideoScheduleAudit(c, videoScheduleAuditState(c), false)
			require.NoError(t, err)
			assert.False(t, audit.Run.SnapshotComplete)
			encoded, err = common.Marshal(audit)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "sensitive.invalid")
			assert.NotContains(t, string(encoded), "sensitive-key-fixture")
			input.Candidates[0].Name = strings.Repeat("bounded fixture ", 30000)
			appendVideoScheduleRecord(c, VideoScheduleRecord{Input: &input, Mode: "on"}, DecideVideoSchedule(input).Board)
			audit, _, err = buildVideoScheduleAudit(c, videoScheduleAuditState(c), false)
			require.NoError(t, err)
			last := audit.Decisions[len(audit.Decisions)-1]
			assert.False(t, last.SnapshotComplete)
			assert.Empty(t, last.InputJSON)
			assert.Empty(t, last.BoardJSON)
		})
	}
}

// An opt-in, fixed-size capacity measurement with a real database. A barrier
// holds the first write until the burst is queued, then verifies full recovery.
func TestVideoScheduleAuditWriterCapacity(t *testing.T) {
	if os.Getenv("P51_CAPACITY") != "1" {
		t.Skip("set P51_CAPACITY=1 for the representative audit writer measurement")
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "capacity.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previous := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = previous; require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.VideoScheduleRun{}, &model.VideoScheduleDecision{}))
	input := videoDecisionTestInput()
	healthy := videosched.HealthStat{Rate: 1, Samples: 20}
	for id := 2; id <= 20; id++ {
		input.Candidates = append(input.Candidates, videoDecisionTestCandidate(id, 0, healthy, healthy))
	}
	choice := DecideVideoSchedule(input)
	fingerprint, _, err := VideoDecisionFingerprint(input)
	require.NoError(t, err)
	for _, recoverBacklog := range []bool{false, true} {
		t.Run(fmt.Sprint("recovery=", recoverBacklog), func(t *testing.T) {
			writer := newVideoAuditWriter()
			entered, recovered := make(chan struct{}), make(chan struct{})
			if recoverBacklog {
				first := true
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("capacity_barrier", func(tx *gorm.DB) {
					if first {
						first = false
						close(entered)
						select {
						case <-recovered:
						case <-tx.Statement.Context.Done():
							tx.AddError(tx.Statement.Context.Err())
						}
					}
				}))
			}
			started := time.Now()
			writer.start()
			maxPending, maxBytes := 0, 0
			for i := range 250 {
				c := newVideoSchedTestContext(t)
				c.Set(common.RequestIdKey, fmt.Sprintf("capacity-%t-%d", recoverBacklog, i))
				decision := VideoSchedDecision{Takeover: true}
				common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
				freezeVideoScheduleAudit(c, decision, true)
				for range 3 {
					appendVideoScheduleRecord(c, VideoScheduleRecord{Input: &input, Fingerprint: fingerprint, Recommended: choice.Best.ID, Mode: "on"}, choice.Board)
				}
				audit, size, err := buildVideoScheduleAudit(c, videoScheduleAuditState(c), false)
				require.NoError(t, err)
				require.True(t, writer.enqueue(audit, size))
				if recoverBacklog && i == 0 {
					writer.wake <- struct{}{}
					<-entered
				}
				writer.mu.Lock()
				maxPending, maxBytes = max(maxPending, writer.status.Pending), max(maxBytes, writer.status.PendingBytes)
				writer.mu.Unlock()
			}
			captured := time.Since(started)
			if recoverBacklog {
				close(recovered)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			writer.stopAndFlush(ctx)
			<-writer.done
			assert.EqualValues(t, 250, writer.status.Written)
			assert.Zero(t, writer.status.Dropped)
			assert.Zero(t, writer.status.Pending)
			assert.Zero(t, writer.status.PendingBytes)
			t.Logf("250 requests / 20 candidates / 3 selections: capture=%s total=%s peak_pending=%d peak_estimated_bytes=%d written=%d dropped=%d", captured, time.Since(started), maxPending, maxBytes, writer.status.Written, writer.status.Dropped)
			if recoverBacklog {
				require.NoError(t, db.Callback().Create().Remove("capacity_barrier"))
			}
		})
	}
}

// Measures the request-end observer separately from upstream network latency.
// The fixed 20-candidate / 3-selection workload matches the P5.1 capacity gate.
func BenchmarkVideoStabilitySelection(b *testing.B) {
	for _, count := range []int{20, 200} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			input := videoDecisionTestInput()
			input.Policy.SelectionPolicy = videosched.PolicyStabilityCostV2
			input.Policy.MinGenRate, input.Policy.MinOverallRate = .8, .6
			input.Policy.MinMarginRate, input.Policy.StabilityTolerance = .1, .01
			input.Policy.MinSamples = 20
			input.Policy.QualificationTTLSeconds, input.Policy.ValidationPeriodSeconds = 86400, 604800
			now := input.Now.Unix()
			input.Candidates = nil
			for id := range count {
				c := videoDecisionTestCandidate(id+1, 1, videosched.HealthStat{}, videosched.HealthStat{})
				c.Reliability = &videosched.ReliabilitySnapshot{Version: 1, Model: "video", State: videosched.HealthNormal, StateVersion: 1, Integrity: "complete", Qualification: &videosched.ReliabilityEvidence{Version: 1, Source: "window", WindowSeconds: 1800, BatchStart: now - 3600, BatchEnd: now - 1800, AsOf: now, ValidatedAt: now, ExpiresAt: now + 86400, Submitted: 100, Accepted: 100, Succeeded: 100}}
				input.Candidates = append(input.Candidates, c)
			}
			b.ReportAllocs()
			for b.Loop() {
				require.NotNil(b, DecideVideoSchedule(input).Best)
			}
		})
	}
}

func BenchmarkVideoScheduleAuditCapture(b *testing.B) {
	gin.SetMode(gin.TestMode)
	input := videoDecisionTestInput()
	healthy := videosched.HealthStat{Rate: 1, Samples: 20}
	for id := 2; id <= 20; id++ {
		input.Candidates = append(input.Candidates, videoDecisionTestCandidate(id, 0, healthy, healthy))
	}
	choice := DecideVideoSchedule(input)
	fingerprint, _, _ := VideoDecisionFingerprint(input)
	for _, enabled := range []bool{false, true} {
		b.Run(fmt.Sprint("audit=", enabled), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
				c.Set(common.RequestIdKey, "benchmark")
				decision := VideoSchedDecision{Takeover: true}
				common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
				freezeVideoScheduleAudit(c, decision, enabled)
				for range 3 {
					appendVideoScheduleRecord(c, VideoScheduleRecord{Input: &input, Fingerprint: fingerprint, Recommended: choice.Best.ID, Mode: "on"}, choice.Board)
				}
				if enabled {
					if _, _, err := buildVideoScheduleAudit(c, videoScheduleAuditState(c), false); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

func newVideoSchedTestContext(t *testing.T) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func setVideoSchedulingForTest(t *testing.T, mode string, models ...string) {
	t.Helper()
	setting := operation_setting.GetVideoSchedulingSetting()
	saved := *setting
	t.Cleanup(func() { *setting = saved })
	setting.Mode = mode
	setting.Models = models
}

// megabyai and seedance-hjmie are real built-in plugins sharing the model
// names videos-mini/fast/standard and exporting describeSpec; sora does not.
func builtinVideoPlugin(t *testing.T, key string) (*jsplugin.RoutingGeneration, *jsplugin.LoadedPlugin) {
	t.Helper()
	generation := jsplugin.DefaultRegistry.Generation()
	plugin, ok := generation.Get(key)
	require.True(t, ok, key)
	return generation, plugin
}

func TestDecideVideoSchedFreezesOneRequestLevelAnswer(t *testing.T) {
	generation, megabyai := builtinVideoPlugin(t, "megabyai")
	_, seedance := builtinVideoPlugin(t, "seedance-hjmie")
	_, sora := builtinVideoPlugin(t, "sora")
	protocol := func(plugins ...*jsplugin.LoadedPlugin) func(*gin.Context) {
		return func(c *gin.Context) {
			endpoint := jsplugin.PinnedEndpoint{Generation: generation, Plugin: plugins[0], Model: "videos-fast"}
			for _, plugin := range plugins {
				endpoint.Candidates = append(endpoint.Candidates, jsplugin.ProtocolBinding{Plugin: plugin, Protocol: "openai_video", Model: "videos-fast"})
			}
			c.Set(jsplugin.ContextKeyPinnedEndpoint, endpoint)
			c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: plugins[0]})
		}
	}
	native := func(c *gin.Context) {
		c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: seedance})
	}
	originTask := func(c *gin.Context) {
		protocol(megabyai, seedance)(c)
		common.SetContextKey(c, constant.ContextKeyOriginTasks, []*model.Task{{TaskID: "task_origin"}})
	}

	previousReady, previousMemoryCache, previousMaster := videoHealthReady.Load(), common.MemoryCacheEnabled, common.IsMasterNode
	t.Cleanup(func() {
		videoHealthReady.Store(previousReady)
		common.MemoryCacheEnabled, common.IsMasterNode = previousMemoryCache, previousMaster
	})
	common.IsMasterNode = true // a single instance needs no Redis

	for _, tc := range []struct {
		name          string
		mode          string
		models        []string
		entry         func(*gin.Context)
		want          VideoSchedDecision
		uncalibrated  bool
		noMemoryCache bool
	}{
		{"off never inspects candidates", operation_setting.VideoSchedulingModeOff, nil, protocol(megabyai, sora), VideoSchedDecision{Reason: VideoSchedReasonModeOff}, false, false},
		{"shadow observes a hooked pool", operation_setting.VideoSchedulingModeShadow, nil, protocol(megabyai, seedance), VideoSchedDecision{Shadow: true}, false, false},
		{"on takes over a listed hooked pool", operation_setting.VideoSchedulingModeOn, []string{"videos-fast"}, protocol(megabyai, seedance), VideoSchedDecision{Takeover: true}, false, false},
		{"on takes over the native entry", operation_setting.VideoSchedulingModeOn, nil, native, VideoSchedDecision{Takeover: true}, false, false},
		{"model outside the allow list", operation_setting.VideoSchedulingModeOn, []string{"videos-mini"}, protocol(megabyai, seedance), VideoSchedDecision{Reason: VideoSchedReasonModelNotListed}, false, false},
		{"one hook-less candidate blocks the pool", operation_setting.VideoSchedulingModeOn, nil, protocol(megabyai, sora), VideoSchedDecision{Reason: VideoSchedReasonMixedPool}, false, false},
		{"shadow is blocked by a mixed pool too", operation_setting.VideoSchedulingModeShadow, nil, protocol(sora, seedance), VideoSchedDecision{Reason: VideoSchedReasonMixedPool}, false, false},
		{"origin task continuation is not a submission", operation_setting.VideoSchedulingModeOn, nil, originTask, VideoSchedDecision{Reason: VideoSchedReasonNotSubmit}, false, false},
		// Mode on reaches a request only after in-flight gauges were calibrated
		// once and while its runtime prerequisites hold; shadow needs neither.
		{"on before the first calibration", operation_setting.VideoSchedulingModeOn, nil, protocol(megabyai, seedance), VideoSchedDecision{Reason: VideoSchedReasonNotReady}, true, false},
		{"on without the memory cache", operation_setting.VideoSchedulingModeOn, nil, protocol(megabyai, seedance), VideoSchedDecision{Reason: VideoSchedReasonNotReady}, false, true},
		{"shadow before the first calibration", operation_setting.VideoSchedulingModeShadow, nil, protocol(megabyai, seedance), VideoSchedDecision{Shadow: true}, true, false},
		// Shadow reads candidates from the memory cache like on does; without it
		// the candidate snapshot would silently be empty.
		{"shadow without the memory cache", operation_setting.VideoSchedulingModeShadow, nil, protocol(megabyai, seedance), VideoSchedDecision{Reason: VideoSchedReasonNotReady}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setVideoSchedulingForTest(t, tc.mode, tc.models...)
			videoHealthReady.Store(!tc.uncalibrated)
			common.MemoryCacheEnabled = !tc.noMemoryCache
			c := newVideoSchedTestContext(t)
			c.Set("resolved_task_model", "videos-fast")
			tc.entry(c)

			assert.Equal(t, tc.want, DecideVideoSched(c))
			// Changing the global mode afterwards does not move the frozen answer.
			operation_setting.GetVideoSchedulingSetting().Mode = operation_setting.VideoSchedulingModeOff
			assert.Equal(t, tc.want, VideoSchedDecisionFrom(c))
		})
	}

	t.Run("entries without a decision never schedule", func(t *testing.T) {
		assert.Equal(t, VideoSchedDecision{}, VideoSchedDecisionFrom(newVideoSchedTestContext(t)))
		assert.Equal(t, VideoSchedDecision{}, VideoSchedDecisionFrom(nil))
	})
	t.Run("readiness validates the frozen configuration", func(t *testing.T) {
		setVideoSchedulingForTest(t, operation_setting.VideoSchedulingModeOn)
		setting := operation_setting.GetVideoSchedulingSetting()
		frozen := *setting
		frozen.SelectionPolicy = videosched.PolicyStabilityCostV2
		frozen.MinGenRate, frozen.MinOverallRate = .8, .6
		frozen.MinMarginRate, frozen.StabilityTolerance = .1, .01
		frozen.MinSamples, frozen.ExploreMaxInFlight = 20, 1
		frozen.QualificationTTLSeconds, frozen.ValidationPeriodSeconds = 86400, 604800
		setting.SelectionPolicy, setting.MinGenRate = videosched.PolicyStabilityCostV2, .79
		common.MemoryCacheEnabled, common.IsMasterNode = true, true
		videoHealthReady.Store(true)
		c := newVideoSchedTestContext(t)
		c.Set("resolved_task_model", "videos-fast")
		common.SetContextKey(c, constant.ContextKeyVideoSchedSetting, &frozen)
		native(c)
		assert.Equal(t, VideoSchedDecision{Takeover: true}, DecideVideoSched(c))
	})

	for _, tc := range []struct {
		name  string
		cause string
		mode  string
		entry func(*gin.Context)
		want  VideoSchedDecision
	}{
		{"v2 before calibration rejects", "uncalibrated", operation_setting.VideoSchedulingModeOn, protocol(megabyai, seedance), VideoSchedDecision{Takeover: true, Reason: VideoSchedReasonNotReady}},
		{"v2 without memory cache rejects", "cache", operation_setting.VideoSchedulingModeOn, native, VideoSchedDecision{Takeover: true, Reason: VideoSchedReasonNotReady}},
		{"v2 slave without Redis rejects", "redis", operation_setting.VideoSchedulingModeOn, native, VideoSchedDecision{Takeover: true, Reason: VideoSchedReasonNotReady}},
		{"v2 invalid configuration rejects", "settings", operation_setting.VideoSchedulingModeOn, native, VideoSchedDecision{Takeover: true, Reason: VideoSchedReasonNotReady}},
		{"v2 off remains exempt", "uncalibrated", operation_setting.VideoSchedulingModeOff, native, VideoSchedDecision{Reason: VideoSchedReasonModeOff}},
		{"v2 origin continuation remains exempt", "uncalibrated", operation_setting.VideoSchedulingModeOn, originTask, VideoSchedDecision{Reason: VideoSchedReasonNotSubmit}},
		{"v2 mixed pool remains exempt", "uncalibrated", operation_setting.VideoSchedulingModeOn, protocol(megabyai, sora), VideoSchedDecision{Reason: VideoSchedReasonMixedPool}},
		{"v2 unlisted model remains exempt", "model", operation_setting.VideoSchedulingModeOn, native, VideoSchedDecision{Reason: VideoSchedReasonModelNotListed}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setVideoSchedulingForTest(t, tc.mode)
			s := operation_setting.GetVideoSchedulingSetting()
			s.SelectionPolicy = videosched.PolicyStabilityCostV2
			s.MinGenRate, s.MinOverallRate = .8, .6
			s.MinMarginRate, s.StabilityTolerance = .1, .01
			s.MinSamples, s.ExploreMaxInFlight = 20, 1
			s.QualificationTTLSeconds, s.ValidationPeriodSeconds = 86400, 604800
			priorRedis, priorMaster := common.RedisEnabled, common.IsMasterNode
			t.Cleanup(func() { common.RedisEnabled, common.IsMasterNode = priorRedis, priorMaster })
			common.RedisEnabled, common.IsMasterNode = false, true
			common.MemoryCacheEnabled = tc.cause != "cache"
			videoHealthReady.Store(tc.cause != "uncalibrated" && tc.cause != "model")
			switch tc.cause {
			case "redis":
				common.IsMasterNode = false
			case "settings":
				s.MinGenRate = .79
			case "model":
				s.Models = []string{"videos-mini"}
			}
			c := newVideoSchedTestContext(t)
			c.Set("resolved_task_model", "videos-fast")
			tc.entry(c)
			assert.Equal(t, tc.want, DecideVideoSched(c))
			if tc.want.Takeover {
				channel, err := selectVideoChannel(c, "default", "videos-fast", nil)
				assert.Nil(t, channel)
				assert.ErrorIs(t, err, model.ErrTierSelectorNoCandidate)
			}
		})
	}
}

func TestVideoSchedStaticBlockersNameHooklessPluginsSharingAModel(t *testing.T) {
	registry := jsplugin.NewRegistry()
	source, err := plugins.Source("seedance-hjmie")
	require.NoError(t, err)
	_, err = registry.RegisterFactory(source, jsplugin.Options{Key: "seedance-hjmie"})
	require.NoError(t, err)
	_, err = registry.Register(`
export const meta = {apiVersion: 1, key: "hookless-video", name: "hookless-video", version: "1.0.0", author: {name: "Test"},
  models: ["videos-fast"], fetchMode: "per_task", protocols: ["openai_video"]};
export function buildSubmitRequest() { return {url: "https://example.com"}; }
export function parseSubmitResponse() { return {taskId: "one"}; }
export function buildQueryRequest() { return {url: "https://example.com"}; }
export function parseTaskResult() { return {status: "SUCCESS"}; }
export function listArtifacts() { return []; }
export function buildContentRequest() { throw new Error("artifact_not_found"); }
export const protocols = {openai_video: {
  decodeRequest: function(ctx) { return {kind: "submit", model: ctx.model}; },
  render: function() { return {}; },
}};
`, jsplugin.Options{})
	require.NoError(t, err)
	generation := registry.Generation()

	assert.Equal(t, []string{"hookless-video"}, VideoSchedStaticBlockers(t.Context(), generation, "videos-fast"))
	assert.Empty(t, VideoSchedStaticBlockers(t.Context(), generation, "videos-mini"))
	// An empty allow list schedules every model of a hooked plugin; only the
	// shared one is reported.
	assert.Contains(t, videoSchedBlockerWarning(t.Context(), generation, nil), " videos-fast=[hookless-video]")
	assert.NotContains(t, videoSchedBlockerWarning(t.Context(), generation, nil), "videos-mini")
	assert.Empty(t, videoSchedBlockerWarning(t.Context(), generation, []string{"videos-mini"}))
}

func TestEstimateVideoSellMirrorsSubmissionPricing(t *testing.T) {
	generation, seedance := builtinVideoPlugin(t, "seedance-hjmie")
	_, megabyai := builtinVideoPlugin(t, "megabyai")
	billingConfig := config.GlobalConfig.Get("billing_setting")
	savedExpr := config.GlobalConfig.ExportAllConfigs()[billing_setting.PluginBillingExprOption]
	savedPrices := ratio_setting.ModelPrice2JSONString()
	savedGroups := ratio_setting.GroupRatio2JSONString()
	savedGroupGroups := ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, config.UpdateConfigFromMap(billingConfig, map[string]string{"plugin_billing_expr": savedExpr}))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedPrices))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedGroups))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(savedGroupGroups))
	})
	setExpr := func(expr string) {
		require.NoError(t, config.UpdateConfigFromMap(billingConfig, map[string]string{"plugin_billing_expr": `{"seedance-hjmie::videos-fast":` + expr + `}`}))
	}
	setExpr(`"u(\"seconds\") * 0.05"`)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"videos-mini":0.1,"videos-standard":0}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2,"gift":0}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"svip":{"vip":0.5}}`))

	seedanceBody := map[string]any{"prompt": "cat", "duration": 8, "resolution": "720p"}
	megabyaiBody := map[string]any{"prompt": "cat", "duration": 8, "resolution": "720p"}
	pinned := func(c *gin.Context) {
		c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: seedance})
	}

	t.Run("expression price is cached before the group ratio", func(t *testing.T) {
		c := newVideoSchedTestContext(t)
		pinned(c)
		sell := EstimateVideoSell(c, "default", seedance, "videos-fast", "videos-fast", seedanceBody, "text_to_video")
		assert.Equal(t, videosched.SellKnown, sell.Kind)
		assert.InDelta(t, 0.4, sell.USD, 1e-9)
		assert.True(t, sell.Estimated)

		// The request keeps its cached pre-group price even if pricing changes,
		// while every call applies the ratio of the group it is asked about.
		setExpr(`"u(\"seconds\") * 1"`)
		t.Cleanup(func() { setExpr(`"u(\"seconds\") * 0.05"`) })
		assert.InDelta(t, 0.8, EstimateVideoSell(c, "vip", seedance, "videos-fast", "videos-fast", seedanceBody, "").USD, 1e-9)
		common.SetContextKey(c, constant.ContextKeyUserGroup, "svip")
		assert.InDelta(t, 0.2, EstimateVideoSell(c, "vip", seedance, "videos-fast", "videos-fast", seedanceBody, "").USD, 1e-9)
		assert.Equal(t, videosched.SellPrice{Kind: videosched.SellFree, Estimated: true},
			EstimateVideoSell(c, "gift", seedance, "videos-fast", "videos-fast", seedanceBody, ""))
	})

	t.Run("a unified sale ignores plugin pricing and is equal for every candidate", func(t *testing.T) {
		c := newVideoSchedTestContext(t)
		pinned(c)
		SetVideoSalesFacts(c, VideoSalesFacts{Model: "video-unified", Seconds: 15, Resolution: "720p", USDPerSecond: 0.02})
		seedanceSell := EstimateVideoSell(c, "vip", seedance, "videos-fast", "videos-fast", seedanceBody, "")
		megabyaiSell := EstimateVideoSell(c, "vip", megabyai, "videos-mini", "videos-mini", megabyaiBody, "")
		assert.Equal(t, videosched.SellKnown, seedanceSell.Kind)
		assert.False(t, seedanceSell.Estimated)
		assert.InDelta(t, 0.6, seedanceSell.USD, 1e-9, "15 s x $0.02 x vip ratio 2, not the plugin override")
		assert.Equal(t, seedanceSell, megabyaiSell)
	})

	for _, tc := range []struct {
		name  string
		setup func(*gin.Context)
		call  func(*gin.Context) videosched.SellPrice
		want  videosched.SellPrice
	}{
		{"usage hook rejecting the body is unknown", pinned, func(c *gin.Context) videosched.SellPrice {
			return EstimateVideoSell(c, "default", seedance, "videos-fast", "videos-fast", map[string]any{"prompt": "cat", "resolution": "720p"}, "")
		}, videosched.SellPrice{Kind: videosched.SellUnknown}},
		{"shared-model expression outside the plugin schema is unknown", func(c *gin.Context) {
			c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: megabyai})
			setExpr(`"u(\"frames\") * 0.05"`)
			t.Cleanup(func() { setExpr(`"u(\"seconds\") * 0.05"`) })
		}, func(c *gin.Context) videosched.SellPrice {
			return EstimateVideoSell(c, "default", seedance, "videos-fast", "videos-fast", seedanceBody, "")
		}, videosched.SellPrice{Kind: videosched.SellUnknown}},
		// Legacy per-call pricing multiplies every positive usage fact, exactly
		// as submission does: 0.1 x seconds 8 x surcharge_seconds 8.
		{"per-call price times billing ratios", pinned, func(c *gin.Context) videosched.SellPrice {
			return EstimateVideoSell(c, "vip", megabyai, "videos-mini", "videos-mini", megabyaiBody, "")
		}, videosched.SellPrice{Kind: videosched.SellKnown, USD: 12.8, Estimated: true}},
		{"zero per-call price is free", pinned, func(c *gin.Context) videosched.SellPrice {
			return EstimateVideoSell(c, "default", megabyai, "videos-standard", "videos-standard", megabyaiBody, "")
		}, videosched.SellPrice{Kind: videosched.SellFree, Estimated: true}},
		// 8 seconds x $1000 is past the int32 single-request quota, which
		// submission pre-consume rejects, so it can never be sold.
		{"sale past the single-request quota ceiling is unknown", func(c *gin.Context) {
			pinned(c)
			setExpr(`"u(\"seconds\") * 1000"`)
			t.Cleanup(func() { setExpr(`"u(\"seconds\") * 0.05"`) })
		}, func(c *gin.Context) videosched.SellPrice {
			return EstimateVideoSell(c, "default", seedance, "videos-fast", "videos-fast", seedanceBody, "")
		}, videosched.SellPrice{Kind: videosched.SellUnknown, Estimated: true}},
		{"no price configured is unknown", pinned, func(c *gin.Context) videosched.SellPrice {
			return EstimateVideoSell(c, "default", megabyai, "videos-fast", "videos-fast", megabyaiBody, "")
		}, videosched.SellPrice{Kind: videosched.SellUnknown}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newVideoSchedTestContext(t)
			tc.setup(c)
			got := tc.call(c)
			assert.Equal(t, tc.want.Kind, got.Kind)
			assert.InDelta(t, tc.want.USD, got.USD, 1e-9)
			assert.Equal(t, tc.want.Estimated, got.Estimated)
		})
	}
}

// createVideoSchedChannel stores an enabled task plugin channel serving
// videos-fast in group and bound to plugin, with the given settings JSON
// ("" = no video_scheduling) and model mapping ("" = none).
func createVideoSchedChannel(t *testing.T, db *gorm.DB, id int, group, plugin string, priority int64, settings, mapping string) {
	t.Helper()
	weight, autoBan := uint(100), 0
	setting := fmt.Sprintf(`{"task_plugin_key":%q}`, plugin)
	channel := &model.Channel{Id: id, Type: constant.ChannelTypeTaskPlugin, Key: "k", Status: common.ChannelStatusEnabled, Name: fmt.Sprintf("video-%d", id),
		Weight: &weight, Models: "videos-fast", Group: group, Priority: &priority, Setting: &setting, OtherSettings: settings, AutoBan: &autoBan}
	if mapping != "" {
		channel.ModelMapping = &mapping
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, channel.AddAbilities(db))
}

// videoSchedProtocolRequest is a protocol-entry request for videos-fast that
// megabyai and seedance-hjmie both accepted, each with its own decoded body.
func videoSchedProtocolRequest(t *testing.T, body map[string]any, decision VideoSchedDecision) *gin.Context {
	t.Helper()
	generation, megabyai := builtinVideoPlugin(t, "megabyai")
	_, seedance := builtinVideoPlugin(t, "seedance-hjmie")
	c := newVideoSchedTestContext(t)
	endpoint := jsplugin.PinnedEndpoint{Generation: generation, Plugin: megabyai, Model: "videos-fast"}
	for _, plugin := range []*jsplugin.LoadedPlugin{megabyai, seedance} {
		endpoint.Candidates = append(endpoint.Candidates, jsplugin.ProtocolBinding{Plugin: plugin, Protocol: "openai_video", Model: "videos-fast", DecodedBody: body})
	}
	c.Set(jsplugin.ContextKeyPinnedEndpoint, endpoint)
	c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Generation: generation, Plugin: megabyai})
	c.Set("expected_task_plugin_key", "megabyai")
	c.Set("task_request", body)
	common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
	return c
}

func setVideoSellExprForTest(t *testing.T, variants string) {
	t.Helper()
	billingConfig := config.GlobalConfig.Get("billing_setting")
	saved := config.GlobalConfig.ExportAllConfigs()[billing_setting.PluginBillingExprOption]
	t.Cleanup(func() {
		require.NoError(t, config.UpdateConfigFromMap(billingConfig, map[string]string{"plugin_billing_expr": saved}))
	})
	require.NoError(t, config.UpdateConfigFromMap(billingConfig, map[string]string{"plugin_billing_expr": variants}))
}

// megabyai and seedance-hjmie share videos-fast; each channel is quoted with
// the plugin that would execute on it, from that plugin's own decoded body.
func TestVideoSchedulerQuotesEachCandidateWithItsOwnPlugin(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	useVideoHealthBackend(t, "memory")
	operation_setting.GetVideoSchedulingSetting().MaxCostToSellRatio = 1
	setVideoSellExprForTest(t, `{"megabyai::videos-fast":"u(\"seconds\") * 0.1","seedance-hjmie::videos-fast":"u(\"seconds\") * 0.1"}`)
	// megabyai is cheaper per second but buys each reference video for $1;
	// seedance-hjmie's price already includes them.
	createVideoSchedChannel(t, db, 3101, "default", "megabyai", 0,
		`{"video_scheduling":{"models":{"videos-fast":{"mode":"per_second","prices":{"720p":0.05},"references":{"video":{"*":{"mode":"per_input","value":1}}}}}}}`, "")
	createVideoSchedChannel(t, db, 3102, "default", "seedance-hjmie", 0,
		`{"video_scheduling":{"models":{"videos-fast":{"mode":"per_second","prices":{"720p":0.08},"references":{"video":{"*":{"mode":"included"}}}}}}}`, "")
	model.InitChannelCache()
	plugins := map[int]string{3101: "megabyai", 3102: "seedance-hjmie"}

	for _, tc := range []struct {
		name     string
		videos   []any
		want     int
		costs    map[int]float64
		excluded map[int]string
	}{
		{"the base price decides without references", nil, 3101, map[int]float64{3101: 0.4, 3102: 0.64}, nil},
		// 0.4 + 1 against a 0.8 sale is a 1.75 cost/sell ratio.
		{"a reference video reverses it and trips the loss ceiling", []any{"https://example.com/ref.mp4"}, 3102,
			map[int]float64{3101: 1.4, 3102: 0.64}, map[int]string{3101: "cost/sell 1.75 > 1.00"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"prompt": "cat", "duration": 8, "resolution": "720p"}
			if tc.videos != nil {
				body["videos"] = tc.videos
			}
			c := videoSchedProtocolRequest(t, body, VideoSchedDecision{Takeover: true})
			channel, err := model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
			require.NoError(t, err)
			require.NotNil(t, channel)
			assert.Equal(t, tc.want, channel.Id)

			records := VideoScheduleRecords(c)
			require.Len(t, records, 1)
			assert.Equal(t, VideoScheduleRecord{SelectionSeq: 1, AttemptSeq: 1, Mode: "on", Group: "default", Recommended: tc.want}, VideoScheduleRecord{
				SelectionSeq: records[0].SelectionSeq, AttemptSeq: records[0].AttemptSeq, Mode: records[0].Mode, Group: records[0].Group, Recommended: records[0].Recommended})
			require.Len(t, records[0].Candidates, 2)
			for _, row := range records[0].Candidates {
				assert.Equal(t, plugins[row.ID], row.Plugin)
				assert.Equal(t, "videos-fast", row.MappedModel)
				require.NotNil(t, row.Spec)
				assert.Equal(t, len(tc.videos), row.Spec.References["video"])
				assert.Equal(t, videosched.SellKnown, row.SellKind)
				assert.InDelta(t, 0.8, row.SellUSD, 1e-9)
				require.NotNil(t, row.CostUSD)
				assert.InDelta(t, tc.costs[row.ID], *row.CostUSD, 1e-9)
				assert.Equal(t, tc.excluded[row.ID], row.Excluded)
			}
		})
	}
}

const videoSpecProbePlugin = `
export const meta = {apiVersion: 1, key: "spec-probe", name: "spec-probe", version: "1.0.0", author: {name: "Test"}, models: ["videos-fast"], fetchMode: "per_task"};
export function buildSubmitRequest() { return {url: "https://example.com"}; }
export function parseSubmitResponse() { return {taskId: "one"}; }
export function buildQueryRequest() { return {url: "https://example.com"}; }
export function parseTaskResult() { return {status: "SUCCESS"}; }
export function describeSpec(ctx) {
  if (ctx.upstreamModel === "future") return {spec_version: 3, references: {video: 0, image: 0, audio: 0}};
  if (ctx.upstreamModel === "opt-out") return {unsupported: true};
  if (ctx.upstreamModel === "broken") throw new Error("bad body");
  return {spec_version: 1, output_seconds: 5, resolution: "*", references: {video: 0, image: 0, audio: 0}};
}
`

func TestUnifiedVideoCandidatesRequireTheFrozenSaleSpec(t *testing.T) {
	useVideoHealthBackend(t, "memory")
	const source = `
export const meta = {apiVersion:1,key:"unified-spec",name:"Unified spec",version:"1.0.0",author:{name:"Test"},models:["real-video"],fetchMode:"per_task"};
export function buildSubmitRequest(){return {url:"https://example.com"};}
export function parseSubmitResponse(){return {taskId:"one"};}
export function buildQueryRequest(){return {url:"https://example.com"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
export function describeSpec(ctx){return {spec_version:1,output_seconds:ctx.requestBody.seconds,resolution:ctx.requestBody.resolution,references:{video:0,image:0,audio:0}};}
`
	plugin, err := jsplugin.NewRegistry().Register(source, jsplugin.Options{})
	require.NoError(t, err)
	for _, tc := range []struct {
		name, model, mapping, tier, priceTier, excluded string
		seconds                                         int
		allowedSeconds                                  string
	}{
		{name: "alias mapped", model: "video-unified", mapping: `{"video-unified":"real-video"}`, seconds: 15, tier: "720p", priceTier: "720p"},
		{name: "folded alias mapping", model: "Video-Unified", mapping: `{"video-unified":"real-video"}`, seconds: 15, tier: "720p", priceTier: "720p"},
		{name: "known real model without mapping", model: "REAL-VIDEO", seconds: 15, tier: "720p", priceTier: "720p"},
		{name: "unknown public name without mapping", model: "video-unified", seconds: 15, tier: "720p", priceTier: "720p", excluded: "no upstream mapping"},
		{name: "ambiguous folded mapping", model: "video-unified", mapping: `{"Video-Unified":"real-video","video-unified":"other-video"}`, seconds: 15, tier: "720p", priceTier: "720p", excluded: "ambiguous upstream mapping"},
		{name: "wrong seconds", model: "real-video", seconds: 10, tier: "720p", priceTier: "720p", excluded: "spec does not match unified sale"},
		{name: "wrong resolution", model: "real-video", seconds: 15, tier: "1080p", priceTier: "720p", excluded: "spec does not match unified sale"},
		{name: "wildcard tier", model: "real-video", seconds: 15, tier: "*", priceTier: "*", excluded: "spec does not match unified sale"},
		{name: "empty tier", model: "real-video", seconds: 15, tier: "", priceTier: "*", excluded: "resolution must be a non-empty string"},
		{name: "tier without a purchase price", model: "real-video", seconds: 15, tier: "720p", priceTier: "1080p", excluded: "tier 720p not priced"},
		{name: "2160p matches 4k", model: "real-video", seconds: 15, tier: "2160p", priceTier: "4k"},
		{name: "pixels match 4k", model: "real-video", seconds: 15, tier: "3840x2160", priceTier: "4k"},
		{name: "supported resolution duration", model: "real-video", seconds: 15, tier: "720p", priceTier: "720p", allowedSeconds: `{"720p":[5,10,15],"1080p":[5,10]}`},
		{name: "unsupported duration combination", model: "real-video", seconds: 15, tier: "720p", priceTier: "720p", allowedSeconds: `{"720p":[5,10]}`, excluded: "seconds 15 not allowed"},
		{name: "resolution absent from combinations", model: "real-video", seconds: 15, tier: "720p", priceTier: "720p", allowedSeconds: `{"1080p":[15]}`, excluded: "tier 720p has no allowed seconds"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newVideoSchedTestContext(t)
			c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Plugin: plugin})
			c.Set("task_request", map[string]any{"seconds": tc.seconds, "resolution": tc.tier})
			resolution := "720p"
			if tc.priceTier == "4k" {
				resolution = "4k"
			}
			SetVideoSalesFacts(c, VideoSalesFacts{Model: tc.model, Seconds: 15, Resolution: resolution, USDPerSecond: 0.02})
			capabilities := ""
			if tc.allowedSeconds != "" {
				capabilities = `,"allowed_seconds_by_resolution":` + tc.allowedSeconds
			}
			channel := &model.Channel{Id: 551, Models: tc.model, Status: common.ChannelStatusEnabled, ModelMapping: &tc.mapping,
				OtherSettings: fmt.Sprintf(`{"video_scheduling":{"models":{%q:{"mode":"per_video","prices":{%q:0.12}%s}}}}`, tc.model, tc.priceTier, capabilities)}
			candidate, _ := assembleVideoCandidate(c, "default", tc.model, channel, false, operation_setting.GetVideoSchedulingSetting())
			if candidate.Excluded == "" {
				candidate.Excluded = videosched.Quote(candidate.Cost, candidate.Spec).Reason
			}
			if tc.excluded != "" {
				assert.Contains(t, candidate.Excluded, tc.excluded)
				return
			}
			require.Empty(t, candidate.Excluded)
			assert.Equal(t, "real-video", candidate.MappedModel)
			assert.Equal(t, resolution, candidate.Spec.Tier)
			assert.Equal(t, 15.0, *candidate.Spec.OutputSeconds)
			assert.Equal(t, 0.12, videosched.Quote(candidate.Cost, candidate.Spec).TotalUSD)
			assert.InDelta(t, 0.3, candidate.Sell.USD, 1e-12)
		})
	}
}

func TestUnifiedVideoNewAPIChannelChoosesTheMappedTargetsPlugin(t *testing.T) {
	registry := jsplugin.NewRegistry()
	const source = `
export const meta = {apiVersion:1,key:%q,name:"Unified target",version:"1.0.0",author:{name:"Test"},models:%s,fetchMode:"per_task",upstreams:["new_api"],protocols:["openai_video"]};
export function buildSubmitRequest(){return {url:"https://example.com"};}
export function parseSubmitResponse(){return {taskId:"one"};}
export function buildQueryRequest(){return {url:"https://example.com"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
export function listArtifacts(){return [];}
export function buildContentRequest(){return {url:"https://example.com/result.mp4"};}
export const protocols={openai_video:{decodeRequest(ctx){return {kind:"submit",model:ctx.model,requestBody:ctx.body.value};},render(ctx,task){return task.data;}}};
`
	_, err := registry.Register(fmt.Sprintf(source, "unified-alpha", `["target-a","target-alt"]`), jsplugin.Options{})
	require.NoError(t, err)
	_, err = registry.Register(fmt.Sprintf(source, "unified-beta", `["target-b"]`), jsplugin.Options{})
	require.NoError(t, err)
	generation := registry.Generation()
	candidates := append(generation.LookupEndpointCandidates(http.MethodPost, "/v1/videos", "target-a"), generation.LookupEndpointCandidates(http.MethodPost, "/v1/videos", "target-b")...)
	require.Len(t, candidates, 2)
	for _, tc := range []struct{ target, plugin string }{{"target-b", "unified-beta"}, {"target-alt", "unified-alpha"}} {
		t.Run(tc.target, func(t *testing.T) {
			c := newVideoSchedTestContext(t)
			c.Set(jsplugin.ContextKeyPinnedEndpoint, jsplugin.PinnedEndpoint{Generation: generation, Plugin: candidates[0].Plugin, Model: "video-unified", Candidates: candidates})
			SetVideoSalesFacts(c, VideoSalesFacts{Model: "video-unified", Seconds: 15, Resolution: "720p", USDPerSecond: 0.02})
			binding := `{"task_extend_plugin_keys":["unified-alpha","unified-beta"]}`
			mapping := fmt.Sprintf(`{"video-unified":%q}`, tc.target)
			channel := &model.Channel{Type: constant.ChannelTypeNewAPI, Setting: &binding, ModelMapping: &mapping}
			for _, expected := range []string{"unified-alpha", "unified-beta"} {
				candidate, ok := PinnedEndpointCandidateForChannel(c, channel, expected)
				require.True(t, ok)
				assert.Equal(t, tc.plugin, candidate.Plugin.Meta.Key, "selection follows this channel's target, including a plugin's other declared targets")
			}
		})
	}
}

func TestVideoSchedulerExclusionsLayersAndFallback(t *testing.T) {
	db := setupChannelSelectAutoGroupsTest(t)
	useVideoHealthBackend(t, "memory")
	setting := operation_setting.GetVideoSchedulingSetting()
	setting.MinSamples, setting.MinSubmitRate, setting.UnknownSellPolicy = 1, 0.8, "relative"
	setting.ProbeRatio, setting.ProbeMaxInFlight = 0, 1 // subtests turn probing on
	setting.CapacityGroups = map[string]int{"acct": 2}
	plugin, err := jsplugin.NewRegistry().Register(videoSpecProbePlugin, jsplugin.Options{})
	require.NoError(t, err)

	scheduled := func(capacity int, group string) string {
		return fmt.Sprintf(`{"video_scheduling":{"capacity":%d,"capacity_group":%q,"models":{"videos-fast":{"mode":"per_video","prices":{"*":1}}}}}`, capacity, group)
	}
	want := map[int]string{
		// Priority 10: every channel fails a gate the algorithm applies.
		3201: "submit rate 0.00 < 0.80",
		3202: "at capacity", // channel capacity
		3203: "at capacity", // capacity group
		// Priority 5: one eligible channel and every hard exclusion.
		3204: "",
		3205: "not schedulable: no cost table",
		3206: "not schedulable: model not priced",
		3207: "tried",
		3208: "spec version unsupported: 3",
		3209: "not schedulable: model opt-out",
		3210: "spec invalid: ",
	}
	createVideoSchedChannel(t, db, 3201, "default", "spec-probe", 10, scheduled(0, ""), "")
	createVideoSchedChannel(t, db, 3202, "default", "spec-probe", 10, scheduled(1, ""), "")
	createVideoSchedChannel(t, db, 3203, "default", "spec-probe", 10, scheduled(0, "acct"), "")
	createVideoSchedChannel(t, db, 3204, "default", "spec-probe", 5, scheduled(0, "unregistered"), "")
	createVideoSchedChannel(t, db, 3205, "default", "spec-probe", 5, "", "")
	createVideoSchedChannel(t, db, 3206, "default", "spec-probe", 5, `{"video_scheduling":{"models":{"videos-mini":{"mode":"per_video","prices":{"*":1}}}}}`, "")
	createVideoSchedChannel(t, db, 3207, "default", "spec-probe", 5, scheduled(0, ""), "")
	createVideoSchedChannel(t, db, 3208, "default", "spec-probe", 5, scheduled(0, ""), `{"videos-fast":"future"}`)
	createVideoSchedChannel(t, db, 3209, "default", "spec-probe", 5, scheduled(0, ""), `{"videos-fast":"opt-out"}`)
	createVideoSchedChannel(t, db, 3210, "default", "spec-probe", 5, scheduled(0, ""), `{"videos-fast":"broken"}`)
	createVideoSchedChannel(t, db, 3250, "backup", "spec-probe", 5, scheduled(0, ""), "")
	model.InitChannelCache()
	recordVideoSamples(3201, "videos-fast", videoSubmitFail)
	store := videoHealthStore()
	_, err = store.add(videoInFlightKey(3202), 1)
	require.NoError(t, err)
	_, err = store.add(videoGroupInFlightKey("acct"), 2)
	require.NoError(t, err)

	request := func(decision VideoSchedDecision, tried ...int) *gin.Context {
		c := newVideoSchedTestContext(t)
		c.Set(common.RequestIdKey, "real-selection-audit")
		c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Plugin: plugin})
		c.Set("task_request", map[string]any{"prompt": "cat"})
		common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
		freezeVideoScheduleAudit(c, decision, true)
		t.Cleanup(func() {
			if videoScheduleAuditState(c) != nil {
				assertVideoAuditSnapshotMatches(t, c)
			}
		})
		for _, id := range tried {
			RequestPolicy(c).BeginAttempt(&model.Channel{Id: id}, "default")
		}
		return c
	}

	t.Run("empty auto-group visits and cross-group retry retain separate decisions", func(t *testing.T) {
		c := request(VideoSchedDecision{Takeover: true})
		for _, group := range []string{"empty-first", "default", "empty-retry", "backup"} {
			channel, err := model.GetRandomSatisfiedChannelWithContext(c, group, "videos-fast", 0, nil)
			require.NoError(t, err)
			if strings.HasPrefix(group, "empty") {
				assert.Nil(t, channel)
				continue
			}
			require.NotNil(t, channel)
			RequestPolicy(c).BeginAttempt(channel, group)
			if group == "default" {
				captureVideoAuditSubmit(c, channel.Id, &taskdto.TaskError{StatusCode: 502})
			} else {
				captureVideoAuditSubmit(c, channel.Id, nil)
				VideoTaskPersisted(c, &model.Task{ID: 42, TaskID: "cross-group-task", Status: model.TaskStatusSubmitted})
			}
		}
		audit, _, err := buildVideoScheduleAudit(c, videoScheduleAuditState(c), false)
		require.NoError(t, err)
		require.Len(t, audit.Decisions, 4)
		for i, row := range audit.Decisions {
			assert.Equal(t, i+1, row.SelectionSeq)
			assert.Equal(t, i/2+1, row.AttemptSeq)
		}
		assert.Zero(t, audit.Decisions[0].Selected)
		assert.Equal(t, "rejected", audit.Decisions[1].SubmitOutcome)
		assert.Zero(t, audit.Decisions[2].Selected)
		assert.Equal(t, "accepted", audit.Decisions[3].SubmitOutcome)
		assert.Equal(t, "backup", audit.Run.ActualGroup)
		assert.Equal(t, "submitted", audit.Run.RequestOutcome)
		assert.Equal(t, 2, audit.Run.SubmitAttempts)
	})

	t.Run("the highest layer with an eligible candidate wins", func(t *testing.T) {
		c := request(VideoSchedDecision{Takeover: true}, 3207)
		channel, err := model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, 3204, channel.Id, "an unregistered capacity group is no group")
		records := VideoScheduleRecords(c)
		require.Len(t, records, 1)
		assert.Equal(t, 2, records[0].AttemptSeq, "the selection feeds the attempt after the tried one")
		got := map[int]string{}
		for _, row := range records[0].Candidates {
			got[row.ID] = row.Excluded
			if strings.HasPrefix(row.Excluded, "spec invalid: ") {
				got[row.ID] = "spec invalid: "
			}
		}
		assert.Equal(t, want, got)
	})

	t.Run("a frozen takeover keeps its candidates after scheduling is switched off", func(t *testing.T) {
		c := request(VideoSchedDecision{Takeover: true}, 3207)
		setting.Mode = operation_setting.VideoSchedulingModeOff
		t.Cleanup(func() { setting.Mode = operation_setting.VideoSchedulingModeShadow })
		channel, err := model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, 3204, channel.Id)
	})

	t.Run("a gated channel is probed and a lost slot is decided again", func(t *testing.T) {
		setting.ProbeRatio = 1
		lastProbeKey, _ := videoProbeStateKeys(3201)
		restartCooldown := func() { require.NoError(t, videoHealthStore().set(lastProbeKey, 0)) }
		t.Cleanup(func() {
			setting.ProbeRatio = 0
			restartCooldown()
		})

		// 3201 fails the submit gate in the top layer; with the gates relaxed it
		// is the only eligible channel there, so it is probed.
		c := request(VideoSchedDecision{Takeover: true}, 3207)
		channel, err := model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, 3201, channel.Id)
		records := VideoScheduleRecords(c)
		require.Len(t, records, 1)
		assert.True(t, records[0].Probe)
		assert.NotEmpty(t, records[0].Fingerprint)
		lease, leased := peekVideoProbeLease(c)
		require.True(t, leased)
		assert.Equal(t, 3201, lease.ChannelID)
		health, err := GetVideoChannelHealth(3201, "videos-fast", 1)
		require.NoError(t, err)
		assert.Positive(t, health.Probe.LastProbeAt, "the cooldown starts at acquisition")

		// The task inherits the selection, so terminal logs correlate by task.
		RequestPolicy(c).BeginAttempt(channel, "default")
		summary := NewVideoSchedulingSummary(c, channel, "videos-fast")
		require.NotNil(t, summary)
		require.NotNil(t, summary.ProbeSlot)
		assert.Equal(t, lease.Token, summary.ProbeSlot.Token)
		assert.Equal(t, 3201, summary.Selected)
		assert.Equal(t, 1, summary.SelectionSeq)
		assert.Equal(t, 2, summary.AttemptSeq, "3207 was attempted first")
		assert.True(t, summary.Probe)
		require.NotNil(t, summary.Spec)
		assert.Equal(t, map[string]int{"video": 0, "image": 0, "audio": 0}, summary.Spec.References)
		other := model.NewLogOther()
		AppendVideoScheduleConsumeLog(c, &model.Task{ChannelId: 3201}, other)
		adminInfo := other.Snapshot()["admin_info"].(map[string]any)
		schedule := adminInfo["video_schedule"].(map[string]any)
		assert.Equal(t, "on", schedule["mode"])
		assert.Equal(t, 3201, schedule["selected"])
		assert.Equal(t, true, schedule["probe"])
		assert.Len(t, schedule["attempts"], 1)
		assert.NotContains(t, other.Snapshot(), "video_schedule")
		ReleaseUnpersistedVideoProbeLease(c)
		restartCooldown()

		// Another request takes the slot between this one's decision and its
		// admission: the lost choice is recorded, and a fresh snapshot that
		// sees the held slot decides again under a new selection_seq.
		previous := acquireVideoProbeSlot
		t.Cleanup(func() { acquireVideoProbeSlot = previous })
		rival := newVideoSchedTestContext(t)
		acquireVideoProbeSlot = func(_ *gin.Context, channelID, n int, ttl time.Duration) (bool, error) {
			won, err := AcquireVideoProbeSlot(rival, channelID, n, ttl)
			require.NoError(t, err)
			require.True(t, won)
			return false, nil
		}
		c = request(VideoSchedDecision{Takeover: true}, 3207)
		channel, err = model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, 3204, channel.Id)
		records = VideoScheduleRecords(c)
		require.Len(t, records, 2)
		assert.Equal(t, []int{1, 2}, []int{records[0].SelectionSeq, records[1].SelectionSeq})
		assert.Equal(t, []int{2, 2}, []int{records[0].AttemptSeq, records[1].AttemptSeq})
		assert.Equal(t, 3201, records[0].Recommended)
		assert.Equal(t, VideoSchedAdmissionProbeTaken, records[0].Admission)
		assert.Equal(t, 3204, records[1].Recommended)
		assert.False(t, records[1].Probe)
		assert.NotEqual(t, records[0].Fingerprint, records[1].Fingerprint)
		_, leased = peekVideoProbeLease(c)
		assert.False(t, leased)
		ReleaseUnpersistedVideoProbeLease(rival)
		restartCooldown()

		// A slot store that fails to write turns probing off for the rest of
		// the selection rather than failing the request.
		acquireVideoProbeSlot = func(*gin.Context, int, int, time.Duration) (bool, error) {
			return false, errors.New("READONLY")
		}
		c = request(VideoSchedDecision{Takeover: true}, 3207)
		channel, err = model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, 3204, channel.Id)
		records = VideoScheduleRecords(c)
		require.Len(t, records, 2)
		assert.Equal(t, VideoSchedAdmissionProbeError, records[0].Admission)
		assert.False(t, records[1].Probe)
	})

	t.Run("a channel disabled while it was scored is decided again", func(t *testing.T) {
		setting.ProbeRatio = 1
		lastProbeKey, _ := videoProbeStateKeys(3201)
		previous := recheckVideoChannel
		t.Cleanup(func() {
			setting.ProbeRatio = 0
			recheckVideoChannel = previous
			require.NoError(t, videoHealthStore().set(lastProbeKey, 0))
			// Re-enabling only flips the status; a rebuild restores the index.
			model.InitChannelCache()
		})
		// 3201 is chosen for a probe; it is disabled after scoring, before admission.
		recheckVideoChannel = func(group, modelName string, filters []taskdto.ChannelFilter, channelID int) (*model.Channel, bool) {
			if channelID == 3201 {
				model.CacheUpdateChannelStatus(channelID, common.ChannelStatusAutoDisabled)
			}
			return previous(group, modelName, filters, channelID)
		}
		c := request(VideoSchedDecision{Takeover: true}, 3207)
		channel, err := model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Equal(t, 3204, channel.Id)
		assert.Equal(t, common.ChannelStatusEnabled, channel.Status)
		records := VideoScheduleRecords(c)
		require.Len(t, records, 2)
		assert.Equal(t, 3201, records[0].Recommended)
		assert.Equal(t, VideoSchedAdmissionChannelUnavailable, records[0].Admission)
		assert.Equal(t, 3204, records[1].Recommended)
		assert.Empty(t, records[1].Admission)
		for _, row := range records[1].Candidates {
			assert.NotEqual(t, 3201, row.ID, "the fresh snapshot no longer holds the disabled channel")
		}
		_, leased := peekVideoProbeLease(c)
		assert.False(t, leased, "the disabled channel never claims a probe slot")
		last, err := videoHealthStore().get([]string{lastProbeKey})
		require.NoError(t, err)
		assert.Zero(t, last[0], "a probe that was never admitted starts no cooldown")
	})

	t.Run("no eligible candidate never falls back to ordinary selection", func(t *testing.T) {
		channel, err := model.GetRandomSatisfiedChannelWithContext(request(VideoSchedDecision{Takeover: true}, 3204, 3207), "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		assert.Nil(t, channel)
	})

	t.Run("a request not taken over keeps ordinary selection", func(t *testing.T) {
		c := request(VideoSchedDecision{Reason: VideoSchedReasonMixedPool}, 3207)
		channel, err := model.GetRandomSatisfiedChannelWithContext(c, "default", "videos-fast", 0, nil)
		require.NoError(t, err)
		require.NotNil(t, channel)
		assert.Contains(t, []int{3201, 3202, 3203}, channel.Id, "the priority retry index picks the top layer")
		assert.Empty(t, VideoScheduleRecords(c))
	})

	t.Run("shadow scores after ordinary selection and writes no state", func(t *testing.T) {
		// Shadow recommends the probe but never acquires its slot.
		setting.ProbeRatio = 1
		t.Cleanup(func() { setting.ProbeRatio = 0 })
		// fmt prints maps in key order, so equal text is equal state.
		snapshot := func() string {
			memoryVideoHealth.mu.Lock()
			defer memoryVideoHealth.mu.Unlock()
			return fmt.Sprint(memoryVideoHealth.hashes, memoryVideoHealth.gauges, memoryVideoHealth.slots)
		}
		before := snapshot()
		c := request(VideoSchedDecision{Shadow: true})
		channel, selectGroup, selectErr := SelectChannelForRequest(c, "videos-fast", &RetryParam{Ctx: c, TokenGroup: "default", ModelName: "videos-fast", Retry: common.GetPointer(0)})
		require.Nil(t, selectErr)
		require.NotNil(t, channel)
		assert.Contains(t, []int{3201, 3202, 3203}, channel.Id)
		records := VideoScheduleRecords(c)
		require.Len(t, records, 1)
		assert.Equal(t, "shadow", records[0].Mode)
		assert.Equal(t, selectGroup, records[0].Group)
		assert.Equal(t, channel.Id, records[0].Selected)
		assert.Equal(t, 3201, records[0].Recommended)
		assert.True(t, records[0].Probe)
		assert.False(t, records[0].AffinityHit)
		assert.Equal(t, before, snapshot())
		_, leased := peekVideoProbeLease(c)
		assert.False(t, leased)

		// The probe is hypothetical: whichever channel ordinary selection
		// submitted to, its task is no probe, while the recommendation keeps
		// the flag.
		if RequestPolicy(c).Attempts == 0 {
			RequestPolicy(c).BeginAttempt(channel, selectGroup)
		}
		for _, id := range []int{3201, 3202, 3203} {
			submitted, err := model.CacheGetChannel(id)
			require.NoError(t, err)
			summary := NewVideoSchedulingSummary(c, submitted, "videos-fast")
			require.NotNil(t, summary)
			assert.Equal(t, id, summary.Selected)
			assert.False(t, summary.Probe, id)
			assert.Nil(t, summary.ProbeSlot, id)
			other := model.NewLogOther()
			AppendVideoScheduleConsumeLog(c, &model.Task{ChannelId: id}, other)
			schedule := other.Snapshot()["admin_info"].(map[string]any)["video_schedule"].(map[string]any)
			assert.Equal(t, false, schedule["probe"], id)
			attempts := schedule["attempts"].([]VideoScheduleRecord)
			require.Len(t, attempts, 1)
			assert.True(t, attempts[0].Probe, "the recommendation keeps its probe flag")
		}
	})

	t.Run("takeover skips session affinity and shadow keeps it", func(t *testing.T) {
		affinity := operation_setting.GetChannelAffinitySetting()
		previousAffinity := *affinity
		t.Cleanup(func() { *affinity = previousAffinity })
		snapshot, err := model.BuildRequestPolicy(map[string]string{
			"channel_affinity_setting.enabled": "true",
			"channel_affinity_setting.rules":   `[{"name":"session","model_regex":[".*"],"key_sources":[{"type":"request_header","key":"X-Session"}]}]`,
		})
		require.NoError(t, err)
		*affinity = snapshot.Affinity
		session := func(decision VideoSchedDecision) *gin.Context {
			c := request(decision)
			c.Request.Header.Set("X-Session", t.Name())
			return c
		}
		seed := session(VideoSchedDecision{})
		_, found := GetPreferredChannelByAffinity(seed, "videos-fast", "default")
		require.False(t, found)
		seed.Set("channel_id", 3201)
		RecordChannelAffinity(seed, 3201)
		t.Cleanup(func() { ClearCurrentChannelAffinityCache(seed) })

		shadow := session(VideoSchedDecision{Shadow: true})
		channel, _, selectErr := SelectChannelForRequest(shadow, "videos-fast", &RetryParam{Ctx: shadow, TokenGroup: "default", ModelName: "videos-fast", Retry: common.GetPointer(0)})
		require.Nil(t, selectErr)
		assert.Equal(t, 3201, channel.Id)
		records := VideoScheduleRecords(shadow)
		require.Len(t, records, 1)
		assert.True(t, records[0].AffinityHit)
		assert.Equal(t, 3204, records[0].Recommended)

		takeover := session(VideoSchedDecision{Takeover: true})
		channel, _, selectErr = SelectChannelForRequest(takeover, "videos-fast", &RetryParam{Ctx: takeover, TokenGroup: "default", ModelName: "videos-fast", Retry: common.GetPointer(0)})
		require.Nil(t, selectErr)
		assert.Equal(t, 3204, channel.Id)
		bound, found := GetPreferredChannelByAffinity(seed, "videos-fast", "default")
		assert.True(t, found, "the scheduler neither reads nor clears the binding")
		assert.Equal(t, 3201, bound)
	})
}

// A request video scheduling took over leaves 429 and 5xx to the health gate;
// the disable decision and the policy audit event read the same answer.
func TestShouldDisableChannelForRequestYieldsTransientFailuresToScheduling(t *testing.T) {
	previousEnabled, previousRanges := common.AutomaticDisableChannelEnabled, operation_setting.AutomaticDisableStatusCodeRanges
	t.Cleanup(func() {
		common.AutomaticDisableChannelEnabled, operation_setting.AutomaticDisableStatusCodeRanges = previousEnabled, previousRanges
	})
	common.AutomaticDisableChannelEnabled = true
	operation_setting.AutomaticDisableStatusCodeRanges = []operation_setting.StatusCodeRange{{Start: 401, End: 401}, {Start: 429, End: 429}, {Start: 500, End: 599}}

	for _, tc := range []struct {
		name     string
		takeover bool
		status   int
		message  string
		want     bool
	}{
		{"takeover 502", true, http.StatusBadGateway, "bad gateway", false},
		{"takeover 429", true, http.StatusTooManyRequests, "slow down", false},
		{"takeover quota exhausted", true, http.StatusTooManyRequests, "You exceeded your current quota", true},
		{"takeover credential failure", true, http.StatusUnauthorized, "unauthorized", true},
		{"ordinary 502", false, http.StatusBadGateway, "bad gateway", true},
		{"ordinary 429", false, http.StatusTooManyRequests, "slow down", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newVideoSchedTestContext(t)
			common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, VideoSchedDecision{Takeover: tc.takeover, Shadow: !tc.takeover})
			c.Set("auto_ban", true)
			apiErr := types.NewOpenAIError(errors.New(tc.message), types.ErrorCodeBadResponseStatusCode, tc.status) // as task submission reports upstream failures
			assert.Equal(t, tc.want, ShouldDisableChannelForRequest(c, apiErr))

			RecordPolicyFailure(c, 1, apiErr, PolicyDecision{Action: "retry"})
			events := RequestPolicy(c).Events()
			health := "unchanged"
			if tc.want {
				health = "channel_disable_requested"
			}
			assert.Equal(t, health, events[len(events)-1].Health)
		})
	}
}

// videoDecisionTestInput is a hand-built decision input: channel 1 is healthy
// and proven, every other channel is added by the case. Probing and
// exploration are off unless a case turns them on.
func videoDecisionTestInput(candidates ...videosched.Candidate) VideoDecisionInput {
	healthy := videosched.HealthStat{Rate: 1, Samples: 20}
	return VideoDecisionInput{
		Candidates: append([]videosched.Candidate{videoDecisionTestCandidate(1, 0, healthy, healthy)}, candidates...),
		Policy: videosched.Policy{
			Weights:       videosched.DefaultWeights,
			MinSubmitRate: 0.8, MinGenRate: 0.5, MinSamples: 10,
			UnknownSellPolicy: videosched.UnknownSellExclude,
			MaxCostUSD:        1e6,
		},
		Explore:       VideoExploreSettings{ProbeCooldownSec: 300, ProbeMaxInFlight: 1, ExploreMaxInFlight: 2},
		Probe:         map[int]VideoProbeState{},
		SlotOccupancy: map[int]int{},
		Now:           time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Seed:          7,
	}
}

func videoDecisionTestCandidate(id int, priority int64, submit, gen videosched.HealthStat) videosched.Candidate {
	return videosched.Candidate{
		ID: id, Name: fmt.Sprintf("video-%d", id), Priority: priority, Weight: 1, Quality: 0.5,
		Cost:   videosched.CostConfig{Mode: videosched.ModePerVideo, Prices: map[string]float64{"*": 1}},
		Spec:   videosched.Spec{Tier: "*", References: map[string]int{"video": 0, "image": 0, "audio": 0}},
		Sell:   videosched.SellPrice{Kind: videosched.SellKnown, USD: 10},
		Submit: submit, Gen: gen,
	}
}

func TestDecideVideoScheduleProbesAndExplores(t *testing.T) {
	healthy := videosched.HealthStat{Rate: 1, Samples: 20}
	failing := videosched.HealthStat{Rate: 0, Samples: 20}
	gated := func(id int, priority int64) videosched.Candidate {
		return videoDecisionTestCandidate(id, priority, failing, healthy)
	}
	unproven := func(id, samples int) videosched.Candidate {
		return videoDecisionTestCandidate(id, 0, videosched.HealthStat{Rate: 1, Samples: samples}, videosched.HealthStat{Rate: 1, Samples: samples})
	}
	probing := func(input *VideoDecisionInput) { input.Explore.ProbeRatio = 1 }

	for _, tc := range []struct {
		name     string
		extra    []videosched.Candidate
		setup    func(*VideoDecisionInput)
		want     int
		probe    bool
		explore  bool
		excluded map[int]string
	}{
		{"a gated channel gets a probe despite a lower score", []videosched.Candidate{gated(2, 0)}, probing, 2, true, false, nil},
		{"no probe draw keeps it gated", []videosched.Candidate{gated(2, 0)}, nil, 1, false, false, map[int]string{2: "submit rate 0.00 < 0.80"}},
		{"a disabled channel is never probed", []videosched.Candidate{gated(2, 0)}, func(input *VideoDecisionInput) {
			probing(input)
			input.Candidates[1].Excluded = "disabled"
		}, 1, false, false, map[int]string{2: "disabled"}},
		{"a tried channel is never probed", []videosched.Candidate{gated(2, 0)}, func(input *VideoDecisionInput) {
			probing(input)
			input.Candidates[1].Excluded = "tried"
		}, 1, false, false, nil},
		{"an at-capacity channel is never probed", []videosched.Candidate{gated(2, 0)}, func(input *VideoDecisionInput) {
			probing(input)
			input.Candidates[1].Capacity, input.Candidates[1].InFlight = 1, 1
		}, 1, false, false, nil},
		{"an unpriced channel is never probed", []videosched.Candidate{gated(2, 0)}, func(input *VideoDecisionInput) {
			probing(input)
			input.Candidates[1].Cost.Prices = nil
		}, 1, false, false, nil},
		{"the cooldown doubles per consecutive failure", []videosched.Candidate{gated(2, 0)}, func(input *VideoDecisionInput) {
			probing(input)
			input.Probe[2] = VideoProbeState{LastProbeAt: input.Now.Add(-9 * time.Minute).Unix(), ConsecutiveFails: 1}
		}, 1, false, false, nil},
		{"an elapsed cooldown probes again", []videosched.Candidate{gated(2, 0)}, func(input *VideoDecisionInput) {
			probing(input)
			input.Probe[2] = VideoProbeState{LastProbeAt: input.Now.Add(-10 * time.Minute).Unix(), ConsecutiveFails: 1}
		}, 2, true, false, nil},
		{"held probe slots block the probe", []videosched.Candidate{gated(2, 0)}, func(input *VideoDecisionInput) {
			probing(input)
			input.SlotOccupancy[2] = 1
		}, 1, false, false, nil},
		{"a lower layer is never probed across a live higher layer", []videosched.Candidate{gated(2, -1)}, probing, 1, false, false, nil},
		{"a higher layer holding only gated channels is probed", []videosched.Candidate{gated(2, 5)}, probing, 2, true, false, nil},
		{"with proven channels the fewest-samples unproven one is explored", []videosched.Candidate{unproven(3, 5), unproven(4, 1)}, func(input *VideoDecisionInput) {
			input.Explore.ExploreShare = 1
		}, 4, false, true, nil},
		{"without an explore draw unproven channels stay out of the argmax", []videosched.Candidate{unproven(3, 5), unproven(4, 1)}, nil, 1, false, false,
			map[int]string{3: "unproven", 4: "unproven"}},
		{"without proven channels unproven ones are capped per channel", []videosched.Candidate{unproven(3, 0), unproven(4, 0)}, func(input *VideoDecisionInput) {
			input.Candidates = input.Candidates[1:]
			input.Candidates[0].Quality, input.Candidates[0].InFlight = 1, 2
		}, 4, false, false, map[int]string{3: "explore limit"}},
		{"a zero explore cap leaves unproven channels uncapped", []videosched.Candidate{unproven(3, 0), unproven(4, 0)}, func(input *VideoDecisionInput) {
			input.Candidates = input.Candidates[1:]
			input.Candidates[0].Quality, input.Candidates[0].InFlight = 1, 2
			input.Explore.ExploreMaxInFlight = 0
		}, 3, false, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := videoDecisionTestInput(tc.extra...)
			if tc.setup != nil {
				tc.setup(&input)
			}
			choice := DecideVideoSchedule(input)
			require.NotNil(t, choice.Best)
			assert.Equal(t, tc.want, choice.Best.ID)
			assert.Equal(t, tc.probe, choice.Probe)
			assert.Equal(t, tc.explore, choice.Explore)
			for id, reason := range tc.excluded {
				for _, score := range choice.Board {
					if score.Candidate.ID == id {
						assert.Equal(t, reason, score.Reason, "channel %d", id)
					}
				}
			}
			assert.Equal(t, choice, DecideVideoSchedule(input), "the same input decides the same")
			c := newVideoSchedTestContext(t)
			c.Set(common.RequestIdKey, "probe-explore-audit")
			decision := VideoSchedDecision{Takeover: true}
			common.SetContextKey(c, constant.ContextKeyVideoSchedDecision, decision)
			freezeVideoScheduleAudit(c, decision, true)
			fingerprint, _, err := VideoDecisionFingerprint(input)
			require.NoError(t, err)
			appendVideoScheduleRecord(c, VideoScheduleRecord{Input: &input, Fingerprint: fingerprint, Recommended: choice.Best.ID, Probe: choice.Probe, Explore: choice.Explore, Mode: "on"}, choice.Board)
			assertVideoAuditSnapshotMatches(t, c)
		})
	}
}

// A retained complete snapshot must reproduce the actual selector input hash,
// and no selection may overwrite another selection within the same attempt.
func assertVideoAuditSnapshotMatches(t *testing.T, c *gin.Context) {
	t.Helper()
	records := VideoScheduleRecords(c)
	audit, _, err := buildVideoScheduleAudit(c, videoScheduleAuditState(c), false)
	require.NoError(t, err)
	require.Len(t, audit.Decisions, len(records))
	for i, row := range audit.Decisions {
		assert.Equal(t, records[i].SelectionSeq, row.SelectionSeq)
		assert.Equal(t, records[i].AttemptSeq, row.AttemptSeq)
		assert.Equal(t, records[i].AffinityHit, row.AffinityHit)
		assert.Equal(t, records[i].Admission, row.Admission)
		require.NotNil(t, records[i].Input)
		if !row.SnapshotComplete {
			assert.Equal(t, "incomplete_snapshot", audit.Run.DataIssue)
			continue
		}
		var input VideoDecisionInput
		require.NoError(t, common.UnmarshalJsonStr(string(row.InputJSON), &input))
		fingerprint, _, err := VideoDecisionFingerprint(input)
		require.NoError(t, err)
		assert.Equal(t, records[i].Fingerprint, fingerprint)
	}
}

// The fingerprint covers every input of the decision, probe and explore
// included, and the segment hashes name the part that changed.
func TestVideoDecisionFingerprintPinsEveryInput(t *testing.T) {
	encoded, err := common.Marshal(map[string]int{"b": 1, "a": 2})
	require.NoError(t, err)
	require.Equal(t, `{"a":2,"b":1}`, string(encoded), "canonical JSON relies on common.Marshal sorting map keys")

	base := func() VideoDecisionInput {
		input := videoDecisionTestInput(videoDecisionTestCandidate(2, 0, videosched.HealthStat{Rate: 0, Samples: 20}, videosched.HealthStat{Rate: 1, Samples: 20}))
		input.Probe[2] = VideoProbeState{LastProbeAt: 100, ConsecutiveFails: 1}
		return input
	}
	whole, segments, err := VideoDecisionFingerprint(base())
	require.NoError(t, err)
	again, againSegments, err := VideoDecisionFingerprint(base())
	require.NoError(t, err)
	assert.Equal(t, whole, again)
	assert.Equal(t, segments, againSegments)
	assert.ElementsMatch(t, []string{"candidates", "settings", "probe", "slots", "now", "seed"}, slices.Collect(maps.Keys(segments)))

	for _, tc := range []struct {
		name    string
		segment string
		change  func(*VideoDecisionInput)
	}{
		{"last probe time", "probe", func(input *VideoDecisionInput) {
			input.Probe[2] = VideoProbeState{LastProbeAt: 101, ConsecutiveFails: 1}
		}},
		{"consecutive failures", "probe", func(input *VideoDecisionInput) {
			input.Probe[2] = VideoProbeState{LastProbeAt: 100, ConsecutiveFails: 2}
		}},
		{"probe cooldown", "settings", func(input *VideoDecisionInput) { input.Explore.ProbeCooldownSec = 301 }},
		{"probe ratio", "settings", func(input *VideoDecisionInput) { input.Explore.ProbeRatio = 0.5 }},
		{"probe slots", "settings", func(input *VideoDecisionInput) { input.Explore.ProbeMaxInFlight = 2 }},
		{"explore share", "settings", func(input *VideoDecisionInput) { input.Explore.ExploreShare = 0.2 }},
		{"explore cap", "settings", func(input *VideoDecisionInput) { input.Explore.ExploreMaxInFlight = 3 }},
		{"cost bound", "settings", func(input *VideoDecisionInput) { input.Policy.MaxCostUSD = 2e6 }},
		{"selection policy", "settings", func(input *VideoDecisionInput) { input.Policy.SelectionPolicy = videosched.PolicyStabilityCostV2 }},
		{"qualification lifetime", "settings", func(input *VideoDecisionInput) { input.Policy.QualificationTTLSeconds = 86400 }},
		{"validation period", "settings", func(input *VideoDecisionInput) { input.Policy.ValidationPeriodSeconds = 604800 }},
		{"reliability state", "candidates", func(input *VideoDecisionInput) {
			input.Candidates[1].Reliability = &videosched.ReliabilitySnapshot{Version: 1, State: videosched.HealthUnverified, StateVersion: 2, ValidationRound: 3, Integrity: "complete", Model: "video", Reason: "insufficient_samples"}
		}},
		{"slot occupancy", "slots", func(input *VideoDecisionInput) { input.SlotOccupancy[2] = 1 }},
		{"clock", "now", func(input *VideoDecisionInput) { input.Now = input.Now.Add(time.Second) }},
		{"seed", "seed", func(input *VideoDecisionInput) { input.Seed = 8 }},
		{"in-flight count", "candidates", func(input *VideoDecisionInput) { input.Candidates[1].InFlight = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := base()
			tc.change(&input)
			changed, changedSegments, err := VideoDecisionFingerprint(input)
			require.NoError(t, err)
			assert.NotEqual(t, whole, changed)
			for name, hash := range segments {
				if name == tc.segment {
					assert.NotEqual(t, hash, changedSegments[name], name)
				} else {
					assert.Equal(t, hash, changedSegments[name], name)
				}
			}
		})
	}
}
func TestVideoCostKeyNormalizationKeepsChannelSchedulable(t *testing.T) {
	for _, key := range []string{"videos-fast", "VIDEOS-FAST"} {
		t.Run(key, func(t *testing.T) {
			db := setupChannelSelectAutoGroupsTest(t)
			useVideoHealthBackend(t, "memory")
			videoReliabilityCache.Clear()
			t.Cleanup(func() { videoReliabilityCache.Clear() })
			require.NoError(t, db.AutoMigrate(&model.VideoHealthRegistration{}, &model.VideoHealthState{}, &model.VideoHealthAttempt{}, &model.VideoHealthRequest{}, &model.Task{}))
			s := operation_setting.GetVideoSchedulingSetting()
			s.Mode, s.SelectionPolicy, s.MinGenRate = "on", videosched.PolicyStabilityCostV2, .8
			s.ExploreShare, s.ProbeRatio = 0, 0
			savedPrices := ratio_setting.ModelPrice2JSONString()
			t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedPrices)) })
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"videos-fast":2}`))
			plugin, err := jsplugin.NewRegistry().Register(videoSpecProbePlugin, jsplugin.Options{})
			require.NoError(t, err)
			createVideoSchedChannel(t, db, 9001, "default", "spec-probe", 1, fmt.Sprintf(`{"video_scheduling":{"quality":0.8,"models":{%q:{"mode":"per_video","prices":{"*":1}}}}}`, key), "")
			require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 9001).Update("models", "videos-fast,videos-fast@temperature:0.2").Error)
			RefreshVideoReliability(t.Context())
			var channel model.Channel
			require.NoError(t, db.First(&channel, 9001).Error)
			require.NoError(t, db.Model(&model.VideoHealthState{}).Where("channel_id = ? AND model_name = ?", 9001, "videos-fast@temperature:0.2").Update("state", videosched.HealthBlocked).Error)
			publishPersistedVideoReliability(t.Context(), 9001, "videos-fast@temperature:0.2")
			view, err := GetVideoHealthView(&channel, true)
			require.NoError(t, err)
			require.Len(t, view.Models, 2, "every priced channel model must remain visible even when cost keys are shared")
			require.NotNil(t, view.Models["videos-fast@temperature:0.2"].Reliability)
			assert.Equal(t, videosched.HealthBlocked, view.Models["videos-fast@temperature:0.2"].Reliability.State)
			c := newVideoSchedTestContext(t)
			c.Set(jsplugin.ContextKeyPinnedPlugin, jsplugin.PinnedPlugin{Plugin: plugin})
			c.Set("task_request", map[string]any{"prompt": "fixture"})
			input := AssembleVideoDecision(c, s, "default", "videos-fast", []*model.Channel{&channel}, 7)
			require.Len(t, input.Candidates, 1)
			assert.Empty(t, input.Candidates[0].Excluded, "cost lookup accepts the normalized key")
			choice := DecideVideoSchedule(input)
			t.Logf("cost_key=%s quote=%+v flow=%s reason=%s health=%+v", key, videosched.Quote(input.Candidates[0].Cost, input.Candidates[0].Spec), choice.Flow, choice.Reason, input.Candidates[0].Reliability)
			require.NotNil(t, choice.Best, "a recognized cost key must have a matching initialized health state")
		})
	}
}

func TestParseVideoSalesFactsAcceptsOnlyTheSoldSpec(t *testing.T) {
	sales := billing_setting.VideoSalesModel{Resolutions: map[string]billing_setting.VideoSalesTier{
		"720p": {USDPerSecond: 0.02, Seconds: []int{5, 15}},
		"4k":   {USDPerSecond: 0.1, Seconds: []int{5}},
	}}
	jsonBody := func(fields map[string]any) any {
		return map[string]any{"kind": "json", "value": fields}
	}
	formBody := func(fields map[string][]string) any {
		return map[string]any{"kind": "multipart", "fields": fields}
	}
	for _, tc := range []struct {
		name, wantErr string
		body          any
		want          VideoSalesFacts
	}{
		{name: "json size", body: jsonBody(map[string]any{"seconds": float64(15), "size": "1280x720"}), want: VideoSalesFacts{Seconds: 15, Resolution: "720p", USDPerSecond: 0.02}},
		{name: "one explicit output", body: jsonBody(map[string]any{"n": float64(1), "seconds": float64(15), "size": "1280x720"}), want: VideoSalesFacts{Seconds: 15, Resolution: "720p", USDPerSecond: 0.02}},
		{name: "one multipart output", body: formBody(map[string][]string{"n": {"1"}, "seconds": {"5"}, "resolution": {"720p"}}), want: VideoSalesFacts{Seconds: 5, Resolution: "720p", USDPerSecond: 0.02}},
		{name: "multiple outputs", body: jsonBody(map[string]any{"n": float64(2), "seconds": float64(15), "size": "1280x720"}), wantErr: "exactly one output"},
		{name: "zero outputs", body: jsonBody(map[string]any{"n": float64(0), "seconds": float64(15), "size": "1280x720"}), wantErr: "exactly one output"},
		{name: "repeated multipart count", body: formBody(map[string][]string{"n": {"1", "1"}, "seconds": {"5"}, "resolution": {"720p"}}), wantErr: "exactly one output"},
		{name: "portrait size and agreeing aliases", body: jsonBody(map[string]any{"duration": "15", "seconds": float64(15), "size": "720x1280", "resolution": "720P"}), want: VideoSalesFacts{Seconds: 15, Resolution: "720p", USDPerSecond: 0.02}},
		{name: "3840x2160 is 4k", body: jsonBody(map[string]any{"seconds": float64(5), "size": "3840x2160"}), want: VideoSalesFacts{Seconds: 5, Resolution: "4k", USDPerSecond: 0.1}},
		{name: "2160p is 4k", body: formBody(map[string][]string{"seconds": {"5"}, "resolution": {"2160p"}}), want: VideoSalesFacts{Seconds: 5, Resolution: "4k", USDPerSecond: 0.1}},
		{name: "multipart", body: formBody(map[string][]string{"seconds": {"5"}, "resolution": {"720p"}, "prompt": {"a", "b"}}), want: VideoSalesFacts{Seconds: 5, Resolution: "720p", USDPerSecond: 0.02}},
		{name: "seconds conflict", body: jsonBody(map[string]any{"seconds": float64(15), "duration": float64(5), "size": "1280x720"}), wantErr: "conflict"},
		{name: "resolution conflict", body: jsonBody(map[string]any{"seconds": float64(5), "size": "1280x720", "resolution": "4k"}), wantErr: "conflict"},
		{name: "fractional seconds", body: jsonBody(map[string]any{"seconds": 15.5, "size": "1280x720"}), wantErr: "whole number"},
		{name: "decimal string", body: jsonBody(map[string]any{"seconds": "15.0", "size": "1280x720"}), wantErr: "whole number"},
		{name: "zero", body: jsonBody(map[string]any{"seconds": float64(0), "size": "1280x720"}), wantErr: "whole number"},
		{name: "negative", body: jsonBody(map[string]any{"seconds": float64(-5), "size": "1280x720"}), wantErr: "whole number"},
		{name: "huge", body: jsonBody(map[string]any{"seconds": 1.8446744073686646e19, "size": "1280x720"}), wantErr: "whole number"},
		{name: "boolean", body: jsonBody(map[string]any{"seconds": true, "size": "1280x720"}), wantErr: "whole number"},
		{name: "repeated multipart seconds", body: formBody(map[string][]string{"seconds": {"5", "15"}, "resolution": {"720p"}}), wantErr: "whole number"},
		{name: "missing seconds", body: jsonBody(map[string]any{"size": "1280x720"}), wantErr: "seconds is required"},
		{name: "missing resolution", body: jsonBody(map[string]any{"seconds": float64(5)}), wantErr: "resolution or size is required"},
		{name: "bad size", body: jsonBody(map[string]any{"seconds": float64(5), "size": "1280*720"}), wantErr: "WIDTHxHEIGHT"},
		{name: "unsold tier", body: jsonBody(map[string]any{"seconds": float64(5), "size": "1920x1080"}), wantErr: "1080p is not sold"},
		{name: "unsold seconds", body: jsonBody(map[string]any{"seconds": float64(10), "size": "1280x720"}), wantErr: "10 seconds is not sold"},
		{name: "json array", body: map[string]any{"kind": "json", "value": []any{}}, wantErr: "JSON object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts, err := ParseVideoSalesFacts("video-unified", sales, tc.body)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			tc.want.Model = "video-unified"
			assert.Equal(t, tc.want, facts)
		})
	}
}
