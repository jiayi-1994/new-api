package service

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// Failure attribution decides which outcomes say something about a channel.
// Only failures the upstream is responsible for count against it; client
// mistakes, local rejections and cancellations are not samples at all.

// videoSubmitOutcome attributes a submission from its status and error kind
// only. The retry decision is not an input: stops such as pinned channels or
// an exhausted budget do not describe the upstream.
func videoSubmitOutcome(taskErr *taskdto.TaskError) VideoOutcome {
	switch {
	case taskErr == nil:
		return VideoOutcomeSuccess
	case errors.Is(taskErr.Error, relaycommon.ErrTaskSubmitOutcomeUnknown):
		// The upstream may have accepted it, but the request got no task: an
		// upstream that keeps hanging up must not stay invisible to the gates
		// while takeover requests also keep it out of AutoBan.
		return VideoOutcomeFail
	case taskErr.LocalError:
		// Local rejections (including insufficient user quota) never reached
		// the upstream.
		return VideoOutcomeIgnored
	case taskErr.StatusCode/100 == 5, taskErr.StatusCode == http.StatusTooManyRequests,
		taskErr.StatusCode == http.StatusUnauthorized, taskErr.StatusCode == http.StatusForbidden:
		// Upstream faults, rate limits and rejected channel credentials.
		return VideoOutcomeFail
	default:
		// 400, 408, 422 and other client-side statuses describe the request.
		return VideoOutcomeIgnored
	}
}

// Failure classes a plugin's optional classifyFailure hook may return.
const (
	VideoFailureUpstream  = "upstream"
	VideoFailureUser      = "user"
	VideoFailureCancelled = "cancelled"
)

// VideoFailureClassifier is implemented by task adaptors whose plugin exports
// classifyFailure. Anything else, or an invalid answer, counts as upstream.
type VideoFailureClassifier interface {
	ClassifyFailure(reason string) (string, bool)
}

// videoTerminalOutcome attributes a terminal task. hostFailure is a failure
// the host detected itself (timeout, poll failure escalation): its reason is
// host text, so it always counts against the upstream without the plugin.
func videoTerminalOutcome(task *model.Task, hostFailure bool) VideoOutcome {
	outcome, _ := videoTerminalAttribution(task, hostFailure)
	return outcome
}

// The health observer and audit share one classification, so optional plugin
// hooks are never called twice for the same observed terminal transition.
func videoTerminalAttribution(task *model.Task, hostFailure bool) (VideoOutcome, string) {
	switch {
	case task.Status == model.TaskStatusSuccess:
		return VideoOutcomeSuccess, "success"
	case hostFailure:
		return VideoOutcomeFail, "host"
	}
	if GetTaskAdaptorFunc != nil {
		if classifier, ok := GetTaskAdaptorFunc(task.Platform).(VideoFailureClassifier); ok {
			class, ok := classifier.ClassifyFailure(task.FailReason)
			switch {
			case ok && (class == VideoFailureUser || class == VideoFailureCancelled):
				return VideoOutcomeIgnored, class
			case ok && class == VideoFailureUpstream:
				return VideoOutcomeFail, class
			}
		}
	}
	// Unclassified failures count against the upstream; the log line is the
	// sample set for calibrating plugin classifiers before rollout.
	common.SysLog(fmt.Sprintf("video scheduling failure class=unknown task=%s platform=%s channel=%d reason=%q", task.TaskID, task.Platform, task.ChannelId, task.FailReason))
	return VideoOutcomeFail, "unknown"
}
