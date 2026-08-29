package constant

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// 孤儿清扫用「预留记录的年龄」推断「提交请求已经死了」。这个推断只有在提交本身
// 有确定上界、且宽限期显著大于该上界时才成立，否则会退掉仍在提交中的请求。
func TestSetTaskSubmitTimeoutKeepsTheOrphanGraceAboveTheSubmitBound(t *testing.T) {
	originalTimeout := TaskSubmitTimeout
	originalGrace := TaskReservationOrphanGrace
	t.Cleanup(func() {
		TaskSubmitTimeout = originalTimeout
		TaskReservationOrphanGrace = originalGrace
	})

	tests := []struct {
		name      string
		configure time.Duration
		timeout   time.Duration
		grace     time.Duration
	}{
		{name: "default", configure: 5 * time.Minute, timeout: 5 * time.Minute, grace: 15 * time.Minute},
		{name: "short bound keeps the floor", configure: 30 * time.Second, timeout: 30 * time.Second, grace: 15 * time.Minute},
		{name: "long bound pushes the grace out", configure: 30 * time.Minute, timeout: 30 * time.Minute, grace: 40 * time.Minute},
		{name: "non-positive falls back", configure: 0, timeout: 5 * time.Minute, grace: 15 * time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			SetTaskSubmitTimeout(tc.configure)

			assert.Equal(t, tc.timeout, TaskSubmitTimeout)
			assert.Equal(t, tc.grace, TaskReservationOrphanGrace)
			assert.Greater(t, TaskReservationOrphanGrace, TaskSubmitTimeout,
				"a submit that outlives the grace would be refunded while still in flight")
		})
	}
}
