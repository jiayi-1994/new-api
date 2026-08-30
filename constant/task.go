package constant

import "time"

// 分辨率任务的预扣款由孤儿清扫兜底，而清扫是用「记录年龄」推断「请求已经死了」。
// 这个推断只有在提交请求本身有确定上界时才成立：RELAY_TIMEOUT 默认为 0，即上游
// 请求可以无限期阻塞，此时一个仍然活着的提交会被误判成孤儿而退款，随后上游接受
// 任务，就留下一个扣不到费、也查不到的孤儿任务。
//
// 因此这两个值必须成对维护：提交上界要显著小于清扫宽限期。
var (
	// TaskSubmitTimeout 通过 TASK_SUBMIT_TIMEOUT（秒）配置，默认 5 分钟。
	// 调大它会同步推迟清扫，两者的关系由 SetTaskSubmitTimeout 维护。
	TaskSubmitTimeout          = 5 * time.Minute
	TaskReservationOrphanGrace = 15 * time.Minute
)

// SetTaskSubmitTimeout 设置提交上界，并保证清扫宽限期始终显著大于它。
// 超时是有歧义的：上游可能已经接受并计费。放宽这个值可以减少误判，代价是
// 卡住的提交要更久才会释放用户的预扣额度。
func SetTaskSubmitTimeout(timeout time.Duration) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	TaskSubmitTimeout = timeout
	TaskReservationOrphanGrace = timeout + 10*time.Minute
	if TaskReservationOrphanGrace < 15*time.Minute {
		TaskReservationOrphanGrace = 15 * time.Minute
	}
}

type TaskPlatform string

const (
	TaskPlatformSuno       TaskPlatform = "suno"
	TaskPlatformMidjourney              = "mj"
)

const (
	SunoActionMusic  = "MUSIC"
	SunoActionLyrics = "LYRICS"

	TaskActionGenerate          = "generate"
	TaskActionTextGenerate      = "textGenerate"
	TaskActionFirstTailGenerate = "firstTailGenerate"
	TaskActionReferenceGenerate = "referenceGenerate"
	TaskActionRemix             = "remixGenerate"
)

var SunoModel2Action = map[string]string{
	"suno_music":  SunoActionMusic,
	"suno_lyrics": SunoActionLyrics,
}

func IsSunoModel(modelName string) bool {
	_, ok := SunoModel2Action[modelName]
	return ok
}
