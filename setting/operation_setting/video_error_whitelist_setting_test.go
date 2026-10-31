package operation_setting

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

func TestMatchVideoErrorWhitelist(t *testing.T) {
	previous := videoErrorWhitelistSetting.Rules
	t.Cleanup(func() { videoErrorWhitelistSetting.Rules = previous })
	videoErrorWhitelistSetting.Rules = map[string]string{
		"Content Policy":          "",
		"content policy violated": "素材未通过审核",
	}

	display, matched := MatchVideoErrorWhitelist("upstream: CONTENT POLICY blocked")
	assert.True(t, matched)
	assert.Empty(t, display)

	display, matched = MatchVideoErrorWhitelist("Content policy violated by image")
	assert.True(t, matched)
	assert.Equal(t, "素材未通过审核", display, "the longest keyword wins")

	_, matched = MatchVideoErrorWhitelist("timeout")
	assert.False(t, matched)
}

func TestValidateVideoErrorWhitelist(t *testing.T) {
	assert.NoError(t, ValidateVideoErrorWhitelist(`{}`))
	assert.NoError(t, ValidateVideoErrorWhitelist(`{"图片格式错误":"","content policy":"素材未通过审核"}`))
	for _, value := range []string{
		`[]`,
		`{`,
		`{"content policy":1}`,
		`{"err":""}`,
		`{"余额不足":""}`,
		`{"分组 default 无可用渠道":""}`,
		`{"service unavailable":"稍后重试"}`,
		`{" content policy":""}`,
		`{"` + strings.Repeat("a", maxVideoErrorWhitelistTextRunes+1) + `":""}`,
		`{"content policy":"` + strings.Repeat("a", maxVideoErrorWhitelistTextRunes+1) + `"}`,
	} {
		assert.Error(t, ValidateVideoErrorWhitelist(value), value)
	}
}

func TestDefaultVideoErrorWhitelistIsValid(t *testing.T) {
	value, err := common.Marshal(videoErrorWhitelistSetting.Rules)
	assert.NoError(t, err)
	assert.NoError(t, ValidateVideoErrorWhitelist(string(value)))
}

func TestVideoErrorNeverPublicCoversProductionFailures(t *testing.T) {
	for _, message := range []string{
		"余额不足，可用: 1，需要: 2",
		`{"code":"service_quota_unavailable","message":"生成服务额度暂时不足，请稍后重试"}`,
		"当前暂无可用生成资源，请稍后重试。",
		"service capacity is temporarily unavailable",
		"分组 default 下模型 doubao-seedance-2-0 无可用渠道",
		"submission_confirmation_timeout: EOF",
		"图片处理超时；尚未提交视频生成",
		"upstream error: do request failed",
	} {
		assert.True(t, IsVideoErrorNeverPublic(message), message)
	}
	for keyword := range videoErrorWhitelistSetting.Rules {
		assert.False(t, IsVideoErrorNeverPublic(keyword), keyword)
	}
}
