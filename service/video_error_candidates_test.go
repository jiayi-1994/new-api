package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
)

func TestGroupVideoErrorCandidatesMergesAndFilters(t *testing.T) {
	candidates := groupVideoErrorCandidates([]videoFailureRecord{
		{Message: "unsupported frame rate 120 for task_abc", At: 10},
		{Message: "status_code=400, {\"error\":{\"message\":\"unsupported frame rate 240 for task_def\"}}", At: 30},
		{Message: "upstream status 400: {\"error\":{\"message\":\"unsupported frame rate 60 for task_xyz\"}}", At: 20},
		{Message: "video request rejected", At: 5},
		{Message: "余额不足，可用: 1，需要: 2", At: 40},
		{Message: "service capacity is temporarily unavailable", At: 40},
		{Message: "参考素材宽高比必须在 0.4 到 2.5 之间", At: 40},
		{Message: "   ", At: 40},
	})

	assert.Len(t, candidates, 2, "funds, system, whitelisted and blank failures are not offered")
	assert.Equal(t, 3, candidates[0].Count)
	assert.Equal(t, int64(30), candidates[0].LastSeen)
	assert.Equal(t, "unsupported frame rate", candidates[0].Keyword)
	assert.Equal(t, "video request rejected", candidates[1].Keyword)
}

func TestSanitizeUnifiedVideoUpstreamErrorHidesLinksAndStructure(t *testing.T) {
	for input, want := range map[string]string{
		"下载 https://cdn.vendor.com/a.png?sig=1，请重试": "下载 [已隐藏]，请重试",
		"fetch s3://bucket/key failed":              "fetch [已隐藏] failed",
		"see www.vendor-docs.net/help for details":  "see [已隐藏] for details",
		"素材来自cdn.vendor.cn/x/y.mp4 无法读取":            "素材来自[已隐藏] 无法读取",
		"节点 10.0.0.7:8080/v1 拒绝":                    "节点 [已隐藏] 拒绝",
		"节点 [2001:db8::1]:443 拒绝":                   "节点 [已隐藏] 拒绝",
		"联系 ops@vendor.com 处理":                      "联系 [已隐藏] 处理",
		"图片 data:image/png;base64,AAAA 无效":          "图片 [已隐藏] 无效",
		"参考素材宽高比必须在 0.4 到 2.5 之间":                   "参考素材宽高比必须在 0.4 到 2.5 之间",
		"model.capability_mismatch: 不支持参考视频":        "model.capability_mismatch: 不支持参考视频",
		"文件 clip.mp4 格式不支持":                         "文件 clip.mp4 格式不支持",
		`upstream status 422: {"error":{"code":"x","message":"提示词共 5210 字符，最多允许 5000 字符。"}}`:   "提示词共 5210 字符，最多允许 5000 字符。",
		`status_code=400, {"code":"fail_to_fetch_task","message":"{\"message\":\"图片格式不支持\"}"}`: "图片格式不支持",
		`upstream status 400: {"detail":{"field":"fps"}}`:                                      model.UnifiedVideoRequestFailedMessage,
		`处理失败 {"internal":"detail"}`:                                                           model.UnifiedVideoRequestFailedMessage,
	} {
		assert.Equal(t, want, SanitizeUnifiedVideoUpstreamError(input), input)
	}
}
