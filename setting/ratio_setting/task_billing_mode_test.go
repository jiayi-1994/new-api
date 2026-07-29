package ratio_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"

	"github.com/stretchr/testify/require"
)

// IsTaskPerCallBilling 决定视频任务的额度是否乘上时长等 OtherRatios，
// 是一条计费口径契约：配置优先，旧的 TASK_PRICE_PATCH 环境变量仅作兜底。
func TestIsTaskPerCallBilling(t *testing.T) {
	testCases := []struct {
		name      string
		modeJSON  string
		envPatch  []string
		modelName string
		want      bool
	}{
		{
			name:      "默认按秒计费",
			modelName: "sora-2",
			want:      false,
		},
		{
			name:      "显式配置按次计费",
			modeJSON:  `{"sora-2":"per_call"}`,
			modelName: "sora-2",
			want:      true,
		},
		{
			name:      "显式配置按秒计费",
			modeJSON:  `{"sora-2":"per_second"}`,
			modelName: "sora-2",
			want:      false,
		},
		{
			name:      "配置只作用于命中的模型",
			modeJSON:  `{"sora-2":"per_call"}`,
			modelName: "sora-2-pro",
			want:      false,
		},
		{
			name:      "无配置时回落到旧环境变量",
			envPatch:  []string{"sora-2"},
			modelName: "sora-2",
			want:      true,
		},
		{
			name:      "配置优先于旧环境变量",
			modeJSON:  `{"sora-2":"per_second"}`,
			envPatch:  []string{"sora-2"},
			modelName: "sora-2",
			want:      false,
		},
		{
			name:      "compact 后缀回落到通配符配置",
			modeJSON:  `{"*-openai-compact":"per_call"}`,
			modelName: "sora-2-openai-compact",
			want:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			originalPatches := constant.TaskPricePatches
			t.Cleanup(func() {
				constant.TaskPricePatches = originalPatches
				require.NoError(t, UpdateTaskBillingModeByJSONString("{}"))
			})

			constant.TaskPricePatches = tc.envPatch
			modeJSON := tc.modeJSON
			if modeJSON == "" {
				modeJSON = "{}"
			}
			require.NoError(t, UpdateTaskBillingModeByJSONString(modeJSON))

			require.Equal(t, tc.want, IsTaskPerCallBilling(tc.modelName))
		})
	}
}

// GetTaskBillingMode 供模型广场展示使用：命中配置（或旧环境变量）才返回 ok，
// 未配置的模型不返回模式，由调用方按端点推断默认值。
func TestGetTaskBillingMode(t *testing.T) {
	testCases := []struct {
		name      string
		modeJSON  string
		envPatch  []string
		modelName string
		wantMode  string
		wantOK    bool
	}{
		{
			name:      "未配置不返回模式",
			modelName: "sora-2",
			wantOK:    false,
		},
		{
			name:      "显式按秒配置命中",
			modeJSON:  `{"videos-4-720p":"per_second"}`,
			modelName: "videos-4-720p",
			wantMode:  TaskBillingModePerSecond,
			wantOK:    true,
		},
		{
			name:      "显式按次配置命中",
			modeJSON:  `{"videos-4-720p":"per_call"}`,
			modelName: "videos-4-720p",
			wantMode:  TaskBillingModePerCall,
			wantOK:    true,
		},
		{
			name:      "旧环境变量视为按次配置",
			envPatch:  []string{"sora-2"},
			modelName: "sora-2",
			wantMode:  TaskBillingModePerCall,
			wantOK:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			originalPatches := constant.TaskPricePatches
			t.Cleanup(func() {
				constant.TaskPricePatches = originalPatches
				require.NoError(t, UpdateTaskBillingModeByJSONString("{}"))
			})

			constant.TaskPricePatches = tc.envPatch
			modeJSON := tc.modeJSON
			if modeJSON == "" {
				modeJSON = "{}"
			}
			require.NoError(t, UpdateTaskBillingModeByJSONString(modeJSON))

			mode, ok := GetTaskBillingMode(tc.modelName)
			require.Equal(t, tc.wantOK, ok)
			require.Equal(t, tc.wantMode, mode)
		})
	}
}
