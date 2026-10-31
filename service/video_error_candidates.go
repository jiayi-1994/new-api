package service

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/tidwall/gjson"
)

const (
	videoErrorCandidateWindowSeconds = 30 * 24 * 3600
	videoErrorCandidateScanLimit     = 5000
	videoErrorCandidateResultLimit   = 100
	videoErrorPlaceholder            = "\x00"
)

var videoErrorVariablePattern = regexp.MustCompile(`(?i)sk-[a-z0-9_-]+|[0-9a-f]{8}-[0-9a-f-]{20,}|\b(?:task|cgt|req)[_-][a-z0-9_-]+|\b[a-z0-9]{24,}\b|\d+(?:\.\d+)?`)

// VideoErrorCandidate is a recurring public-eligible failure awaiting an
// administrator's decision. Keyword is a suggestion the administrator can edit.
type VideoErrorCandidate struct {
	Keyword  string `json:"keyword"`
	Sample   string `json:"sample"`
	Count    int    `json:"count"`
	LastSeen int64  `json:"last_seen"`
}

// videoFailureRecord is one failure as stored, before any public projection.
type videoFailureRecord struct {
	Message string
	At      int64
}

func ListVideoErrorCandidates() ([]VideoErrorCandidate, error) {
	since := common.GetTimestamp() - videoErrorCandidateWindowSeconds
	tasks, err := model.GetFailedTasksSince(since, videoErrorCandidateScanLimit)
	if err != nil {
		return nil, err
	}
	logs, err := model.GetErrorLogsSince(since, videoErrorCandidateScanLimit)
	if err != nil {
		return nil, err
	}
	records := make([]videoFailureRecord, 0, len(tasks)+len(logs))
	for _, task := range tasks {
		// The whitelist only projects unified video sales; other tasks are noise.
		if task.IsUnifiedVideoSale() && !unifiedVideoHostFailure(task) {
			records = append(records, videoFailureRecord{Message: task.FailReason, At: task.FinishTime})
		}
	}
	for _, log := range logs {
		status := gjson.Get(log.Other, "status_code").Int()
		if gjson.Get(log.Other, "sales_source").String() == billingexpr.SalesSourceVideoRequest && status != 401 && status != 403 {
			records = append(records, videoFailureRecord{Message: log.Content, At: log.CreatedAt})
		}
	}
	return groupVideoErrorCandidates(records), nil
}

// groupVideoErrorCandidates merges failures differing only by IDs, URLs and
// numbers, then drops those already whitelisted or never allowed to be public.
func groupVideoErrorCandidates(records []videoFailureRecord) []VideoErrorCandidate {
	groups := map[string]*VideoErrorCandidate{}
	for _, record := range records {
		message := unifiedVideoUpstreamMessage(record.Message)
		if message == "" || operation_setting.IsVideoErrorNeverPublic(message) {
			continue
		}
		if _, matched := operation_setting.MatchVideoErrorWhitelist(message); matched {
			continue
		}
		template := upstreamLinkPattern.ReplaceAllString(message, videoErrorPlaceholder)
		template = videoErrorVariablePattern.ReplaceAllString(template, videoErrorPlaceholder)
		group, exists := groups[template]
		if !exists {
			group = &VideoErrorCandidate{Keyword: longestStableSegment(template), Sample: SanitizeUnifiedVideoUpstreamError(message)}
			groups[template] = group
		}
		group.Count++
		group.LastSeen = max(group.LastSeen, record.At)
	}
	candidates := make([]VideoErrorCandidate, 0, len(groups))
	for _, group := range groups {
		candidates = append(candidates, *group)
	}
	slices.SortFunc(candidates, func(left, right VideoErrorCandidate) int {
		return cmp.Or(cmp.Compare(right.Count, left.Count), cmp.Compare(left.Keyword, right.Keyword))
	})
	return candidates[:min(len(candidates), videoErrorCandidateResultLimit)]
}

func longestStableSegment(template string) string {
	longest := ""
	for _, segment := range strings.Split(template, videoErrorPlaceholder) {
		segment = strings.TrimSpace(segment)
		if utf8.RuneCountInString(segment) > utf8.RuneCountInString(longest) {
			longest = segment
		}
	}
	return longest
}
