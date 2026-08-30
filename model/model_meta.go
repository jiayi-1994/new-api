package model

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"gorm.io/gorm"
)

const (
	NameRuleExact = iota
	NameRulePrefix
	NameRuleContains
	NameRuleSuffix
)

type BoundChannel struct {
	Name string `json:"name"`
	Type int    `json:"type"`
}

type Model struct {
	Id           int            `json:"id"`
	ModelName    string         `json:"model_name" gorm:"size:128;not null;uniqueIndex:uk_model_name_delete_at,priority:1"`
	Description  string         `json:"description,omitempty" gorm:"type:text"`
	Icon         string         `json:"icon,omitempty" gorm:"type:varchar(128)"`
	Tags         string         `json:"tags,omitempty" gorm:"type:varchar(255)"`
	VendorID     int            `json:"vendor_id,omitempty" gorm:"index"`
	Endpoints    string         `json:"endpoints,omitempty" gorm:"type:text"`
	Status       int            `json:"status" gorm:"default:1"`
	SyncOfficial int            `json:"sync_official" gorm:"default:1"`
	CreatedTime  int64          `json:"created_time" gorm:"bigint"`
	UpdatedTime  int64          `json:"updated_time" gorm:"bigint"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index;uniqueIndex:uk_model_name_delete_at,priority:2"`

	BoundChannels []BoundChannel `json:"bound_channels,omitempty" gorm:"-"`
	EnableGroups  []string       `json:"enable_groups,omitempty" gorm:"-"`
	QuotaTypes    []int          `json:"quota_types,omitempty" gorm:"-"`
	NameRule      int            `json:"name_rule" gorm:"default:0"`

	MatchedModels []string `json:"matched_models,omitempty" gorm:"-"`
	MatchedCount  int      `json:"matched_count,omitempty" gorm:"-"`
}

type ModelNameConflictError struct {
	Name       string
	ExistingID int
}

func (e *ModelNameConflictError) Error() string {
	return fmt.Sprintf("active model %q already exists", e.Name)
}

func modelNamespaceTransaction(db *gorm.DB, transaction func(*gorm.DB) error) error {
	return db.Transaction(transaction, &sql.TxOptions{Isolation: sql.LevelSerializable})
}

// ensureActiveModelNameAvailable performs an indexed locking read inside a
// serializable transaction. Existing rows are locked directly; an absent name
// is protected by the database's serializable predicate/range semantics so
// concurrent creators cannot both commit.
func ensureActiveModelNameAvailable(tx *gorm.DB, name string, excludeID int) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("model name is required")
	}
	query := lockForUpdate(tx).Where("model_name = ?", name)
	if excludeID != 0 {
		query = query.Where("id <> ?", excludeID)
	}
	var existing Model
	result := query.Order("id ASC").Limit(1).Find(&existing)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 0 {
		return &ModelNameConflictError{Name: name, ExistingID: existing.Id}
	}
	return nil
}

func createModelRecord(tx *gorm.DB, model *Model) error {
	now := common.GetTimestamp()
	model.CreatedTime = now
	model.UpdatedTime = now
	status := model.Status
	syncOfficial := model.SyncOfficial
	if err := tx.Create(model).Error; err != nil {
		return err
	}
	return tx.Model(&Model{}).Where("id = ?", model.Id).Updates(map[string]interface{}{
		"status": status, "sync_official": syncOfficial,
	}).Error
}

func insertModelWithActiveNameGuard(db *gorm.DB, model *Model) error {
	return modelNamespaceTransaction(db, func(tx *gorm.DB) error {
		if err := ensureActiveModelNameAvailable(tx, model.ModelName, 0); err != nil {
			return err
		}
		return createModelRecord(tx, model)
	})
}

func (mi *Model) Insert() error {
	return insertModelWithActiveNameGuard(DB, mi)
}

func IsModelNameDuplicated(id int, name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	var cnt int64
	err := DB.Model(&Model{}).Where("model_name = ? AND id <> ?", name, id).Count(&cnt).Error
	return cnt > 0, err
}

func (mi *Model) Update() error {
	videoResolutionPriceOptionMu.Lock()
	defer videoResolutionPriceOptionMu.Unlock()

	mi.UpdatedTime = common.GetTimestamp()
	var publishedValue string
	err := modelNamespaceTransaction(DB, func(tx *gorm.DB) error {
		var current Model
		if err := lockForUpdate(tx).Where("id = ?", mi.Id).First(&current).Error; err != nil {
			return err
		}
		if current.ModelName != mi.ModelName {
			if err := ensureActiveModelNameAvailable(tx, mi.ModelName, current.Id); err != nil {
				return err
			}
		}
		priceOption, priceDocument, err := loadVideoResolutionPriceOptionForLifecycle(tx)
		if err != nil {
			return err
		}
		if current.ModelName != mi.ModelName {
			if prices, ok := priceDocument[current.ModelName]; ok {
				delete(priceDocument, current.ModelName)
				priceDocument[mi.ModelName] = prices
			}
		}
		publishedValue, err = saveVideoResolutionPriceOptionForLifecycle(tx, priceOption, priceDocument)
		if err != nil {
			return err
		}

		// 使用 Select 强制更新所有字段，包括零值
		return tx.Model(&Model{}).Where("id = ?", mi.Id).
			Select("model_name", "description", "icon", "tags", "vendor_id", "endpoints", "status", "sync_official", "name_rule", "updated_time").
			Updates(mi).Error
	})
	if err != nil {
		return err
	}
	return publishVideoResolutionPriceOption(publishedValue)
}

func (mi *Model) Delete() error {
	return DB.Delete(mi).Error
}

func DeleteModelMetaByID(id int) error {
	videoResolutionPriceOptionMu.Lock()
	defer videoResolutionPriceOptionMu.Unlock()

	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		}
	}()

	var current Model
	if err := lockForUpdate(tx).Where("id = ?", id).First(&current).Error; err != nil {
		tx.Rollback()
		return err
	}
	priceOption, priceDocument, err := loadVideoResolutionPriceOptionForLifecycle(tx)
	if err != nil {
		tx.Rollback()
		return err
	}
	delete(priceDocument, current.ModelName)
	publishedValue, err := saveVideoResolutionPriceOptionForLifecycle(tx, priceOption, priceDocument)
	if err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Delete(&Model{}, id).Error; err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	return publishVideoResolutionPriceOption(publishedValue)
}

func loadVideoResolutionPriceOptionForLifecycle(tx *gorm.DB) (Option, map[string]map[string]float64, error) {
	option := Option{Key: ratio_setting.VideoResolutionPriceOptionKey}
	result := lockForUpdate(tx).Where(commonKeyCol+" = ?", option.Key).Find(&option)
	if result.Error != nil {
		return Option{}, nil, result.Error
	}
	if result.RowsAffected == 0 {
		option.Value = "{}"
		if err := tx.Create(&option).Error; err != nil {
			return Option{}, nil, err
		}
	}
	if err := ratio_setting.ValidateVideoResolutionPriceByJSONString(option.Value); err != nil {
		return Option{}, nil, fmt.Errorf("invalid stored video resolution price: %w", err)
	}
	priceDocument := make(map[string]map[string]float64)
	if err := common.Unmarshal([]byte(option.Value), &priceDocument); err != nil {
		return Option{}, nil, err
	}
	return option, priceDocument, nil
}

func saveVideoResolutionPriceOptionForLifecycle(tx *gorm.DB, option Option, priceDocument map[string]map[string]float64) (string, error) {
	value, err := common.Marshal(priceDocument)
	if err != nil {
		return "", err
	}
	if err := ratio_setting.ValidateVideoResolutionPriceByJSONString(string(value)); err != nil {
		return "", err
	}
	option.Value = string(value)
	if err := tx.Save(&option).Error; err != nil {
		return "", err
	}
	return option.Value, nil
}

func GetVendorModelCounts() (map[int64]int64, error) {
	var stats []struct {
		VendorID int64
		Count    int64
	}
	if err := DB.Model(&Model{}).
		Select("vendor_id as vendor_id, count(*) as count").
		Group("vendor_id").
		Scan(&stats).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]int64, len(stats))
	for _, s := range stats {
		m[s.VendorID] = s.Count
	}
	return m, nil
}

func GetAllModels(offset int, limit int) ([]*Model, error) {
	models, _, err := SearchModels("", "", "", "", offset, limit)
	return models, err
}

func GetBoundChannelsByModelsMap(modelNames []string) (map[string][]BoundChannel, error) {
	result := make(map[string][]BoundChannel)
	if len(modelNames) == 0 {
		return result, nil
	}
	type row struct {
		Model string
		Name  string
		Type  int
	}
	var rows []row
	err := DB.Table("channels").
		Select("abilities.model as model, channels.name as name, channels.type as type").
		Joins("JOIN abilities ON abilities.channel_id = channels.id").
		Where("abilities.model IN ? AND abilities.enabled = ?", modelNames, true).
		Distinct().
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		result[r.Model] = append(result[r.Model], BoundChannel{Name: r.Name, Type: r.Type})
	}
	return result, nil
}

func normalizeLookupValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}

func GetPreferredModelOwnerChannelTypes(modelNames []string, groups []string) (map[string]int, error) {
	result := make(map[string]int)
	modelNames = normalizeLookupValues(modelNames)
	if len(modelNames) == 0 {
		return result, nil
	}

	type row struct {
		Model       string
		ChannelType int
	}
	var rows []row

	query := DB.Table("abilities").
		Select("abilities.model as model, channels.type as channel_type").
		Joins("JOIN channels ON abilities.channel_id = channels.id").
		Where("abilities.model IN ? AND abilities.enabled = ? AND channels.status = ?", modelNames, true, common.ChannelStatusEnabled).
		Order("COALESCE(abilities.priority, 0) DESC").
		Order("abilities.weight DESC").
		Order("abilities.channel_id ASC")

	groups = normalizeLookupValues(groups)
	if len(groups) > 0 {
		query = query.Where("abilities."+commonGroupCol+" IN ?", groups)
	}

	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}

	for _, r := range rows {
		if _, ok := result[r.Model]; ok {
			continue
		}
		result[r.Model] = r.ChannelType
	}
	return result, nil
}

func SearchModels(keyword string, vendor string, status string, syncOfficial string, offset int, limit int) ([]*Model, int64, error) {
	var models []*Model
	db := DB.Model(&Model{})
	if keyword != "" {
		like := "%" + keyword + "%"
		db = db.Where("model_name LIKE ? OR description LIKE ? OR tags LIKE ?", like, like, like)
	}
	if vendor != "" {
		if vid, err := strconv.Atoi(vendor); err == nil {
			db = db.Where("models.vendor_id = ?", vid)
		} else {
			db = db.Joins("JOIN vendors ON vendors.id = models.vendor_id").Where("vendors.name LIKE ?", "%"+vendor+"%")
		}
	}
	if statusValue, ok := parseModelStatusFilter(status); ok {
		db = db.Where("models.status = ?", statusValue)
	}
	if syncValue, ok := parseModelSyncFilter(syncOfficial); ok {
		db = db.Where("models.sync_official = ?", syncValue)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := db.Order("models.id DESC").Offset(offset).Limit(limit).Find(&models).Error; err != nil {
		return nil, 0, err
	}
	return models, total, nil
}

// parseModelStatusFilter maps UI/API status values to the models.status column.
// Returns ok=false when no status filter should be applied.
func parseModelStatusFilter(status string) (value int, ok bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "all":
		return 0, false
	case "enabled", "1":
		return 1, true
	case "disabled", "0":
		return 0, true
	default:
		n, err := strconv.Atoi(status)
		if err != nil {
			return 0, false
		}
		return n, true
	}
}

// parseModelSyncFilter maps UI/API sync values to the models.sync_official column.
// Returns ok=false when no sync filter should be applied.
func parseModelSyncFilter(syncOfficial string) (value int, ok bool) {
	switch strings.ToLower(strings.TrimSpace(syncOfficial)) {
	case "", "all":
		return 0, false
	case "yes", "1":
		return 1, true
	case "no", "0":
		return 0, true
	default:
		n, err := strconv.Atoi(syncOfficial)
		if err != nil {
			return 0, false
		}
		return n, true
	}
}
