package service

import (
	"errors"
	"net/http"

	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// Failure attribution decides which outcomes say something about a channel.
// Only failures the upstream is responsible for count against it; client
// mistakes, local rejections, cancellations and unknown submit outcomes are
// not samples at all.

// videoSubmitOutcome attributes a submission from its status and error kind
// only. The retry decision is not an input: stops such as pinned channels or
// an exhausted budget do not describe the upstream.
func videoSubmitOutcome(taskErr *taskdto.TaskError) VideoOutcome {
	switch {
	case taskErr == nil:
		return VideoOutcomeSuccess
	case taskErr.LocalError, errors.Is(taskErr.Error, relaycommon.ErrTaskSubmitOutcomeUnknown):
		// Local rejections (including insufficient user quota) never reached
		// the upstream; an unknown outcome may still have been accepted.
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

func videoTerminalOutcome(task *model.Task, timedOut bool) VideoOutcome {
	switch {
	case task.Status == model.TaskStatusSuccess:
		return VideoOutcomeSuccess
	case timedOut:
		return VideoOutcomeFail
	}
	if GetTaskAdaptorFunc != nil {
		if classifier, ok := GetTaskAdaptorFunc(task.Platform).(VideoFailureClassifier); ok {
			if class, ok := classifier.ClassifyFailure(task.FailReason); ok && (class == VideoFailureUser || class == VideoFailureCancelled) {
				return VideoOutcomeIgnored
			}
		}
	}
	return VideoOutcomeFail
}
