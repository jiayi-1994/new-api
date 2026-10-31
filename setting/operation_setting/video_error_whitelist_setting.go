package operation_setting

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const (
	VideoErrorWhitelistOptionKey = "video_error_whitelist.rules"

	videoErrorWhitelistSettingName = "video_error_whitelist"
	maxVideoErrorWhitelistRules    = 500
	// Shorter keywords such as "error" would expose nearly every failure.
	minVideoErrorWhitelistMatchRunes = 4
	maxVideoErrorWhitelistTextRunes  = 500
)

// VideoErrorWhitelistSetting maps a keyword found in an upstream failure to the
// text shown to buyers; empty text shows the sanitized upstream message. The
// map is replaced whole on every save, so concurrent readers keep a stable one.
type VideoErrorWhitelistSetting struct {
	Rules map[string]string `json:"rules"`
}

// The defaults are failures buyers can fix themselves, taken from production
// logs. Empty text shows the upstream wording, which already explains the fix.
var videoErrorWhitelistSetting = VideoErrorWhitelistSetting{Rules: map[string]string{
	"提示词共":                                        "提示词长度超过限制，请缩短后重新提交。",
	"提示词不能超过":                                     "",
	"参考素材宽高比必须在":                                  "",
	"无法满足参考素材要求":                                  "",
	"资源不存在或已过期":                                   "",
	"at least one reference material is required": "该模型至少需要一个参考素材，请添加后重新提交。",
	"内容审核未通过":                                     "",
	"生成画面可能涉及版权":                                  "",
	"related to copyright restrictions":           "生成内容可能涉及版权限制，请调整提示词或参考素材后重试。",
	"provider_moderation":                         "内容未通过审核，请调整提示词或参考素材后重试。",
}}

var (
	// UpstreamQuotaMessagePattern identifies upstream account and funds wording.
	UpstreamQuotaMessagePattern = regexp.MustCompile(`(?i)额度|余额|余量|欠费|欠款|充值|扣费|quota|balance|insufficient|credit|billing|overdue|arrear|[$￥¥€£]\s*\d|\d(?:\.\d+)?\s*(?:USD|CNY|RMB|EUR|GBP)\b`)
	videoSystemFailurePattern   = regexp.MustCompile(`(?i)无可用渠道|no available channel|model_not_found|暂无可用|暂时不可用|capacity|unavailable|超时|timeout|timed out|限流|繁忙|rate.?limit|too many requests|未找到路径|do request failed|video scheduling`)
)

var ErrVideoErrorNeverPublic = errors.New("系统或上游账户类报错不能加入白名单")

// IsVideoErrorNeverPublic reports failures that are never whitelisted, whatever
// the rules say: buyers cannot fix them and they reveal the supply side.
func IsVideoErrorNeverPublic(message string) bool {
	return UpstreamQuotaMessagePattern.MatchString(message) || videoSystemFailurePattern.MatchString(message)
}

func init() {
	config.GlobalConfig.Register(videoErrorWhitelistSettingName, &videoErrorWhitelistSetting)
}

// MatchVideoErrorWhitelist returns the display text of the rule matching
// message, ignoring case. The longest keyword wins so that the map's random
// iteration order never changes the result.
func MatchVideoErrorWhitelist(message string) (display string, matched bool) {
	message = strings.ToLower(message)
	longest := ""
	for keyword, text := range videoErrorWhitelistSetting.Rules {
		if !strings.Contains(message, strings.ToLower(keyword)) {
			continue
		}
		if !matched || len(keyword) > len(longest) || (len(keyword) == len(longest) && keyword < longest) {
			longest, display, matched = keyword, text, true
		}
	}
	return display, matched
}

func ValidateVideoErrorWhitelist(value string) error {
	var rules map[string]string
	if err := common.UnmarshalJsonStr(value, &rules); err != nil {
		return fmt.Errorf("video error whitelist must be a JSON object of strings: %w", err)
	}
	if len(rules) > maxVideoErrorWhitelistRules {
		return fmt.Errorf("video error whitelist allows at most %d rules", maxVideoErrorWhitelistRules)
	}
	for keyword, text := range rules {
		if IsVideoErrorNeverPublic(keyword) {
			return fmt.Errorf("%w: %s", ErrVideoErrorNeverPublic, keyword)
		}
		keywordRunes := utf8.RuneCountInString(keyword)
		if keyword != strings.TrimSpace(keyword) || keywordRunes < minVideoErrorWhitelistMatchRunes || keywordRunes > maxVideoErrorWhitelistTextRunes {
			return fmt.Errorf("video error whitelist keyword %q must be trimmed and %d to %d characters long", keyword, minVideoErrorWhitelistMatchRunes, maxVideoErrorWhitelistTextRunes)
		}
		if utf8.RuneCountInString(text) > maxVideoErrorWhitelistTextRunes {
			return fmt.Errorf("video error whitelist text for %q exceeds %d characters", keyword, maxVideoErrorWhitelistTextRunes)
		}
	}
	return nil
}
