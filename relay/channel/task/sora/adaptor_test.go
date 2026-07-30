package sora

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskAdaptorParseTaskResultMapsNonTerminalStatuses(t *testing.T) {
	tests := []struct {
		name           string
		upstreamStatus string
		expectedStatus model.TaskStatus
	}{
		{name: "unknown remains queued", upstreamStatus: "unknown", expectedStatus: model.TaskStatusQueued},
		{name: "submitted remains submitted", upstreamStatus: "submitted", expectedStatus: model.TaskStatusSubmitted},
		{name: "created remains submitted", upstreamStatus: "created", expectedStatus: model.TaskStatusSubmitted},
		{name: "status is normalized", upstreamStatus: "  UNKNOWN  ", expectedStatus: model.TaskStatusQueued},
		{name: "processing is unchanged", upstreamStatus: "processing", expectedStatus: model.TaskStatusInProgress},
		{name: "completed is unchanged", upstreamStatus: "completed", expectedStatus: model.TaskStatusSuccess},
		{name: "failed is unchanged", upstreamStatus: "failed", expectedStatus: model.TaskStatusFailure},
	}

	adaptor := &TaskAdaptor{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := common.Marshal(map[string]any{"status": test.upstreamStatus})
			require.NoError(t, err)

			taskInfo, err := adaptor.ParseTaskResult(body)
			require.NoError(t, err)
			assert.Equal(t, string(test.expectedStatus), taskInfo.Status)
		})
	}
}
