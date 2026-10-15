package model

// TaskRequestBody keeps the client's submit payload outside tasks. Dashboard
// lists load it separately only when recorded video display facts are missing.
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

// GetTaskRequestBodiesByTaskIDs loads display fallbacks for already-authorized tasks.
func GetTaskRequestBodiesByTaskIDs(taskIDs []int64) (map[int64]*TaskRequestBody, error) {
	records := make(map[int64]*TaskRequestBody, len(taskIDs))
	for start := 0; start < len(taskIDs); start += 500 {
		var batch []*TaskRequestBody
		if err := DB.Select("task_row_id", "content_type", "body").
			Where("task_row_id IN ?", taskIDs[start:min(start+500, len(taskIDs))]).Find(&batch).Error; err != nil {
			return nil, err
		}
		for _, record := range batch {
			records[record.TaskRowID] = record
		}
	}
	return records, nil
}
