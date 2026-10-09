package model

// TaskRequestBody keeps the client's submit payload outside tasks, so list,
// polling and ownership queries never load it.
type TaskRequestBody struct {
	ID          int64  `json:"-" gorm:"primaryKey"`
	TaskRowID   int64  `json:"-" gorm:"uniqueIndex"` // tasks.id
	ContentType string `json:"content_type" gorm:"type:varchar(255)"`
	Size        int64  `json:"size"` // original body size in bytes
	Body        string `json:"body" gorm:"type:text"`
}

func GetTaskRequestBody(taskRowID int64) (*TaskRequestBody, bool, error) {
	var record TaskRequestBody
	exist, err := RecordExist(DB.Where("task_row_id = ?", taskRowID).First(&record).Error)
	if err != nil || !exist {
		return nil, exist, err
	}
	return &record, true, nil
}
