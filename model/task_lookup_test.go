package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetByTaskRecordIDIsOwnerScoped(t *testing.T) {
	truncateTables(t)
	task := &Task{TaskID: "legacy-upstream-task-id", UserId: 42, Status: TaskStatusSuccess}
	insertTask(t, task)

	found, exists, err := GetByTaskRecordID(42, task.ID)

	require.NoError(t, err)
	require.True(t, exists)
	require.NotNil(t, found)
	assert.Equal(t, task.ID, found.ID)
	assert.Equal(t, task.TaskID, found.TaskID)

	_, otherOwnerExists, err := GetByTaskRecordID(7, task.ID)
	require.NoError(t, err)
	assert.False(t, otherOwnerExists)
}

func TestGetByTaskRecordIDRejectsNonPositiveID(t *testing.T) {
	truncateTables(t)

	for _, taskRecordID := range []int64{0, -1} {
		task, exists, err := GetByTaskRecordID(42, taskRecordID)
		require.NoError(t, err)
		assert.False(t, exists)
		assert.Nil(t, task)
	}
}

func TestTaskQueriesFilterByRecordID(t *testing.T) {
	truncateTables(t)
	target := &Task{TaskID: "legacy-upstream-task-id", UserId: 42, Status: TaskStatusSuccess}
	insertTask(t, target)
	insertTask(t, &Task{TaskID: "other-task", UserId: 42, Status: TaskStatusSuccess})
	insertTask(t, &Task{TaskID: "another-task", UserId: 7, Status: TaskStatusSuccess})

	query := SyncTaskQueryParams{TaskRecordID: target.ID}
	adminTasks := TaskGetAllTasks(0, 10, query)
	require.Len(t, adminTasks, 1)
	assert.Equal(t, target.ID, adminTasks[0].ID)
	assert.EqualValues(t, 1, TaskCountAllTasks(query))

	userTasks := TaskGetAllUserTask(42, 0, 10, query)
	require.Len(t, userTasks, 1)
	assert.Equal(t, target.ID, userTasks[0].ID)
	assert.EqualValues(t, 1, TaskCountAllUserTask(42, query))
	assert.Empty(t, TaskGetAllUserTask(7, 0, 10, query))
	assert.Zero(t, TaskCountAllUserTask(7, query))
}
