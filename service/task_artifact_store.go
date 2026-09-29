package service

import (
	"context"
	"errors"
	"io"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// StoredArtifactRef describes a persisted artifact object. It is the same
// value the task row keeps in TaskPrivateData.StoredArtifact.
type StoredArtifactRef = model.StoredTaskArtifact

// TaskArtifactStore is the persistence boundary for generated artifact bytes.
// types.TaskArtifact is re-exported by relay/channel as channel.TaskArtifact.
// Resolve returns nil when the task has no usable stored copy (never stored,
// different artifact, or past retention). Persist only uploads; the caller
// records the returned ref on the task. Serve must not write a response when
// it returns an error so callers can fall back to the upstream path.
type TaskArtifactStore interface {
	Enabled() bool
	Resolve(task *model.Task, artifactKey string) (*StoredArtifactRef, error)
	Persist(ctx context.Context, task *model.Task, artifact types.TaskArtifact, content io.Reader) (*StoredArtifactRef, error)
	Serve(c *gin.Context, task *model.Task, ref *StoredArtifactRef) error
}

var ErrTaskArtifactStoreDisabled = errors.New("task artifact store is disabled")

type disabledArtifactStore struct{}

func (disabledArtifactStore) Enabled() bool {
	return false
}

func (disabledArtifactStore) Resolve(*model.Task, string) (*StoredArtifactRef, error) {
	return nil, nil
}

func (disabledArtifactStore) Persist(context.Context, *model.Task, types.TaskArtifact, io.Reader) (*StoredArtifactRef, error) {
	return nil, ErrTaskArtifactStoreDisabled
}

func (disabledArtifactStore) Serve(*gin.Context, *model.Task, *StoredArtifactRef) error {
	return ErrTaskArtifactStoreDisabled
}

var (
	taskArtifactStore       TaskArtifactStore = &disabledArtifactStore{}
	taskArtifactStoreConfig system_setting.TaskArtifactStoreConfig
)

func init() {
	ConfigureTaskArtifactStore(system_setting.LoadTaskArtifactStoreConfig())
}

// ConfigureTaskArtifactStore installs the backend selected by config and
// returns a function that restores the previous backend.
func ConfigureTaskArtifactStore(config system_setting.TaskArtifactStoreConfig) (restore func()) {
	previousStore, previousConfig := taskArtifactStore, taskArtifactStoreConfig
	taskArtifactStoreConfig = config
	if config.Mode == system_setting.TaskArtifactStoreModeS3 {
		taskArtifactStore = newS3ArtifactStore(config)
	} else {
		taskArtifactStore = &disabledArtifactStore{}
	}
	return func() {
		taskArtifactStore, taskArtifactStoreConfig = previousStore, previousConfig
	}
}

// GetTaskArtifactStore returns the process-wide artifact storage backend.
func GetTaskArtifactStore() TaskArtifactStore {
	return taskArtifactStore
}

// GetTaskArtifactStoreConfig returns the validated startup configuration the
// store was built from.
func GetTaskArtifactStoreConfig() system_setting.TaskArtifactStoreConfig {
	return taskArtifactStoreConfig
}
