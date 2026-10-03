package service

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/videosched"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// notifyVideoSchedAlert is the test seam both video scheduling alerts send through.
var notifyVideoSchedAlert = NotifyRootUser

const (
	videoSchedAlertTTL = 10 * time.Minute
	// videoSchedNoChannelKey holds the groups one takeover selection found no
	// admissible channel in; CacheGetRandomSatisfiedChannel resets it per call.
	videoSchedNoChannelKey = "video_sched_no_channel_groups"
)

// stashVideoSchedNoChannel records why group had no admissible channel, for
// the request-level alert sent only when the whole selection comes up empty.
func stashVideoSchedNoChannel(c *gin.Context, group string, candidates int, choice VideoScheduleChoice) {
	detail := "该分组下无启用的渠道"
	if candidates > 0 {
		// Board reasons include the gates and capacity, not only assembly exclusions.
		tally := map[string]int{}
		for _, score := range choice.Board {
			tally[cmp.Or(score.Reason, "eligible")]++
		}
		parts := make([]string, 0, len(tally))
		for _, reason := range slices.Sorted(maps.Keys(tally)) {
			parts = append(parts, fmt.Sprintf("%s×%d", reason, tally[reason]))
		}
		detail = fmt.Sprintf("候选渠道 %d 个：%s", candidates, strings.Join(parts, ", "))
	}
	if choice.Reason != "" {
		detail += "；调度结论：" + choice.Reason
	}
	c.Set(videoSchedNoChannelKey, append(c.GetStringSlice(videoSchedNoChannelKey), fmt.Sprintf("分组「%s」：%s", group, detail)))
}

// notifyVideoSchedNoChannel tells root that a taken-over request found no
// admissible channel for modelName under tokenGroup ("auto" included), at most
// once per pair and TTL cluster-wide. It sends asynchronously so the request
// never waits on SMTP.
func notifyVideoSchedNoChannel(c *gin.Context, tokenGroup, modelName string) {
	groups := c.GetStringSlice(videoSchedNoChannelKey)
	if len(groups) == 0 {
		return
	}
	won, err := videoHealthStore().acquire(videoSchedKeyPrefix+"alert:none:"+tokenGroup+":"+modelName, common.GetRandomString(16), videoSchedAlertTTL)
	if err != nil {
		logger.LogWarn(c, "video scheduling no-channel alert throttle failed: %v", err)
		return
	}
	if !won {
		return
	}
	subject := fmt.Sprintf("视频模型「%s」在分组「%s」无可用渠道", modelName, tokenGroup)
	content := fmt.Sprintf("视频调度在分组「%s」中找不到模型「%s」的可用渠道：\n- %s", tokenGroup, modelName, strings.Join(groups, "\n- "))
	notify, notifyType := notifyVideoSchedAlert, fmt.Sprintf("video_sched_no_channel_%s_%s", tokenGroup, modelName)
	gopool.Go(func() { notify(notifyType, subject, content) })
}

// NotifyNewlyBlockedVideoChannels tells root about channel models whose video
// health entered blocked in the last ten minutes, once per block cluster-wide,
// in at most one digest per five minutes. Only mode on gates traffic on these
// states. A re-block that keeps BlockedAt (recovery expiry, activation) belongs
// to an outage already reported.
func NotifyNewlyBlockedVideoChannels(ctx context.Context, now int64) error {
	if operation_setting.GetVideoSchedulingSetting().Mode != operation_setting.VideoSchedulingModeOn {
		return nil
	}
	var states []model.VideoHealthState
	if err := model.DB.WithContext(ctx).Where("state = ? AND blocked_at > ?", videosched.HealthBlocked, now-600).Find(&states).Error; err != nil {
		return err
	}
	if len(states) == 0 {
		return nil
	}
	// Names are read before any dedupe key is taken, so a failed lookup loses no alert.
	ids := make([]int, len(states))
	for i, state := range states {
		ids[i] = state.ChannelID
	}
	var channels []model.Channel
	if err := model.DB.WithContext(ctx).Select("id", "name").Where("id IN ?", ids).Find(&channels).Error; err != nil {
		return err
	}
	names := make(map[int]string, len(channels))
	for _, channel := range channels {
		names[channel.Id] = channel.Name
	}
	// The gate is taken before any row key, so a gated block waits for the next
	// digest. A gate whose rows all turn out reported delays a new block <= 5 min.
	gate, err := videoHealthStore().acquire(videoSchedKeyPrefix+"alert:blocked:digest", common.GetRandomString(16), 5*time.Minute)
	if err != nil || !gate {
		return err
	}
	// Row keys outlive the 10 min lookback, which covers a 5 min gate wait plus a sweep.
	// ponytail: one digest per gate window; aggregate per model ("3/20 blocked") if volume hurts
	// ponytail: the memory store never prunes expired keys; these grow by one per block event.
	var lines strings.Builder
	fresh := 0
	var acquireErr error
	for _, state := range states {
		won, err := videoHealthStore().acquire(fmt.Sprintf("%salert:blocked:%d:%s:%d", videoSchedKeyPrefix, state.ChannelID, state.ModelName, state.BlockedAt), common.GetRandomString(16), 15*time.Minute)
		if err != nil {
			acquireErr = err
			continue
		}
		if !won {
			continue
		}
		fresh++
		name := cmp.Or(names[state.ChannelID], fmt.Sprintf("#%d", state.ChannelID))
		lines.WriteString(fmt.Sprintf("\n- 渠道「%s」（#%d） 模型「%s」，原因：%s", name, state.ChannelID, state.ModelName, state.Reason))
	}
	if fresh > 0 {
		// A unique type per digest keeps NotifyUser's per-type limit out; the gate throttles.
		notifyVideoSchedAlert(fmt.Sprintf("video_sched_blocked_%d", now), fmt.Sprintf("%d 个视频渠道模型进入熔断", fresh), "以下视频渠道模型的健康状态进入 blocked，调度将不再选择它们，直至恢复验证通过："+lines.String())
	}
	return acquireErr
}
