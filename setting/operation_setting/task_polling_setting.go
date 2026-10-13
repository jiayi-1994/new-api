package operation_setting

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	TaskPollingIntervalOptionKey      = "task_polling_setting.interval_seconds"
	DefaultTaskPollingIntervalSeconds = 15
	MinTaskPollingIntervalSeconds     = 3
	MaxTaskPollingIntervalSeconds     = 300
)

// Kept outside the reflection-based settings objects: the runner reads this
// concurrently with option saves and SyncOptions.
var taskPollingIntervalSeconds atomic.Int64

func ParseTaskPollingInterval(value string) (int, error) {
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < MinTaskPollingIntervalSeconds || seconds > MaxTaskPollingIntervalSeconds {
		return 0, fmt.Errorf("task polling interval must be an integer between %d and %d seconds", MinTaskPollingIntervalSeconds, MaxTaskPollingIntervalSeconds)
	}
	return seconds, nil
}

// LoadTaskPollingInterval accepts persisted data, falling back safely when an
// old database contains an invalid value. Writes must validate before saving.
func LoadTaskPollingInterval(value string) error {
	seconds, err := ParseTaskPollingInterval(value)
	if err != nil {
		seconds = DefaultTaskPollingIntervalSeconds
	}
	taskPollingIntervalSeconds.Store(int64(seconds))
	return err
}

func TaskPollingInterval() time.Duration {
	seconds := taskPollingIntervalSeconds.Load()
	if seconds == 0 {
		seconds = DefaultTaskPollingIntervalSeconds
	}
	return time.Duration(seconds) * time.Second
}
