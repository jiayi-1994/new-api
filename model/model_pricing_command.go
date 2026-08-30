package model

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var modelPricingOptionKeys = []string{
	"AudioCompletionRatio", "AudioRatio", "CacheRatio", "CompletionRatio",
	"CreateCacheRatio", "ImageRatio", "ModelPrice", "ModelRatio",
	"TaskBillingMode", ratio_setting.VideoResolutionPriceOptionKey,
	"billing_setting.billing_expr", "billing_setting.billing_mode",
}

var modelPricingNumericOptionKeys = []string{
	"AudioCompletionRatio", "AudioRatio", "CacheRatio", "CompletionRatio",
	"CreateCacheRatio", "ImageRatio", "ModelPrice", "ModelRatio",
}

var modelPricingStringOptionKeys = []string{
	"TaskBillingMode", "billing_setting.billing_expr", "billing_setting.billing_mode",
}

var modelPricingOptionKeySet = func() map[string]struct{} {
	keys := make(map[string]struct{}, len(modelPricingOptionKeys))
	for _, key := range modelPricingOptionKeys {
		keys[key] = struct{}{}
	}
	return keys
}()

type PricingDocuments struct {
	Numeric         map[string]map[string]float64
	Strings         map[string]map[string]string
	ResolutionPrice map[string]map[string]float64
	Raw             map[string]string
}

type PricingCommandKind string

const (
	PricingCommandSave             PricingCommandKind = "save"
	PricingCommandRename           PricingCommandKind = "rename"
	PricingCommandCopy             PricingCommandKind = "copy"
	PricingCommandDelete           PricingCommandKind = "delete"
	PricingCommandReplaceDocuments PricingCommandKind = "replace_documents"
)

type PricingMode string

const (
	PricingModeFixed           PricingMode = "per_request"
	PricingModeRatio           PricingMode = "per_token"
	PricingModeExpression      PricingMode = "tiered_expr"
	PricingModeVideoResolution PricingMode = "video_resolution"
)

type ModelPricingSelection struct {
	Mode                 PricingMode        `json:"mode"`
	ModelPrice           *float64           `json:"price,omitempty"`
	ModelRatio           *float64           `json:"ratio,omitempty"`
	CacheRatio           *float64           `json:"cache_ratio,omitempty"`
	CreateCacheRatio     *float64           `json:"create_cache_ratio,omitempty"`
	CompletionRatio      *float64           `json:"completion_ratio,omitempty"`
	ImageRatio           *float64           `json:"image_ratio,omitempty"`
	AudioRatio           *float64           `json:"audio_ratio,omitempty"`
	AudioCompletionRatio *float64           `json:"audio_completion_ratio,omitempty"`
	BillingExpr          *string            `json:"billing_expr,omitempty"`
	TaskBillingMode      *string            `json:"task_billing_mode,omitempty"`
	ResolutionPrices     map[string]float64 `json:"resolution_prices,omitempty"`
}

type ModelRowMutation struct {
	Kind  string
	Model *Model
	ID    int
}

type ModelPricingCommand struct {
	Kind              PricingCommandKind
	SourceName        string
	TargetName        string
	Selection         *ModelPricingSelection
	ModelMutation     *ModelRowMutation
	Values            map[string]string
	ExpectedDocuments map[string]string
}

type ModelPricingCommandResult struct {
	Committed            bool
	PublicationRecovered bool
	PublicationPending   bool
	Values               map[string]string
}

type OptionConflictError struct {
	Key          string
	CurrentValue string
}

func (e *OptionConflictError) Error() string {
	return fmt.Sprintf("option %q changed since it was read", e.Key)
}

type pricingTransactionMutation func(tx *gorm.DB, documents *PricingDocuments) (map[string]string, error)

// publishPricingDocumentsAfterCommit is the failpoint-bearing publication
// boundary. Recovery deliberately bypasses it and calls the validated
// low-level publisher directly.
var publishPricingDocumentsAfterCommit = publishPricingDocumentsWithHooks

func currentPricingOptionValue(key string) string {
	common.OptionMapRWMutex.RLock()
	value, ok := common.OptionMap[key]
	common.OptionMapRWMutex.RUnlock()
	if ok && value != "" {
		return value
	}

	switch key {
	case "AudioCompletionRatio":
		return ratio_setting.AudioCompletionRatio2JSONString()
	case "AudioRatio":
		return ratio_setting.AudioRatio2JSONString()
	case "CacheRatio":
		return ratio_setting.CacheRatio2JSONString()
	case "CompletionRatio":
		return ratio_setting.CompletionRatio2JSONString()
	case "CreateCacheRatio":
		return ratio_setting.CreateCacheRatio2JSONString()
	case "ImageRatio":
		return ratio_setting.ImageRatio2JSONString()
	case "ModelPrice":
		return ratio_setting.ModelPrice2JSONString()
	case "ModelRatio":
		return ratio_setting.ModelRatio2JSONString()
	case "TaskBillingMode":
		return ratio_setting.TaskBillingMode2JSONString()
	case ratio_setting.VideoResolutionPriceOptionKey:
		return ratio_setting.VideoResolutionPrice2JSONString()
	case "billing_setting.billing_expr":
		return billing_setting.BillingExpr2JSONString()
	case "billing_setting.billing_mode":
		return billing_setting.BillingMode2JSONString()
	default:
		return "{}"
	}
}

func lockPricingDocuments(tx *gorm.DB) (*PricingDocuments, error) {
	values := make(map[string]string, len(modelPricingOptionKeys))
	for _, key := range modelPricingOptionKeys {
		missing := Option{Key: key, Value: currentPricingOptionValue(key)}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&missing).Error; err != nil {
			return nil, fmt.Errorf("materialize pricing option %q: %w", key, err)
		}
	}

	for _, key := range modelPricingOptionKeys {
		var option Option
		if err := lockForUpdate(tx).Where(&Option{Key: key}).First(&option).Error; err != nil {
			return nil, fmt.Errorf("lock pricing option %q: %w", key, err)
		}
		values[key] = option.Value
	}
	return parsePricingDocuments(values)
}

func requireJSONObject(key, value string) error {
	raw := json.RawMessage(strings.TrimSpace(value))
	if common.GetJsonType(raw) != "object" {
		return fmt.Errorf("pricing option %q must be a JSON object", key)
	}
	if err := common.ValidateJSONNoDuplicateKeys(raw); err != nil {
		return fmt.Errorf("parse pricing option %q: %w", key, err)
	}
	return nil
}

func parsePricingDocuments(values map[string]string) (*PricingDocuments, error) {
	documents := &PricingDocuments{
		Numeric:         make(map[string]map[string]float64, len(modelPricingNumericOptionKeys)),
		Strings:         make(map[string]map[string]string, len(modelPricingStringOptionKeys)),
		ResolutionPrice: make(map[string]map[string]float64),
		Raw:             make(map[string]string, len(modelPricingOptionKeys)),
	}
	for _, key := range modelPricingOptionKeys {
		value, ok := values[key]
		if !ok {
			return nil, fmt.Errorf("missing pricing option %q", key)
		}
		if err := requireJSONObject(key, value); err != nil {
			return nil, err
		}
		documents.Raw[key] = value
	}
	for _, key := range modelPricingNumericOptionKeys {
		var document map[string]float64
		if err := common.Unmarshal([]byte(values[key]), &document); err != nil {
			return nil, fmt.Errorf("parse pricing option %q: %w", key, err)
		}
		if document == nil {
			document = make(map[string]float64)
		}
		for model, value := range document {
			if strings.TrimSpace(model) == "" {
				return nil, fmt.Errorf("pricing option %q contains a blank model key", key)
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, fmt.Errorf("pricing option %q contains a non-finite value for model %q", key, model)
			}
		}
		documents.Numeric[key] = document
	}
	for _, key := range modelPricingStringOptionKeys {
		var document map[string]string
		if err := common.Unmarshal([]byte(values[key]), &document); err != nil {
			return nil, fmt.Errorf("parse pricing option %q: %w", key, err)
		}
		if document == nil {
			document = make(map[string]string)
		}
		for model := range document {
			if strings.TrimSpace(model) == "" {
				return nil, fmt.Errorf("pricing option %q contains a blank model key", key)
			}
		}
		documents.Strings[key] = document
	}
	if err := ratio_setting.ValidateVideoResolutionPriceByJSONString(values[ratio_setting.VideoResolutionPriceOptionKey]); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", ratio_setting.VideoResolutionPriceOptionKey, err)
	}
	if err := common.Unmarshal([]byte(values[ratio_setting.VideoResolutionPriceOptionKey]), &documents.ResolutionPrice); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ratio_setting.VideoResolutionPriceOptionKey, err)
	}
	if documents.ResolutionPrice == nil {
		documents.ResolutionPrice = make(map[string]map[string]float64)
	}
	if err := billing_setting.ValidatePricingDocuments(
		values["billing_setting.billing_mode"],
		values["billing_setting.billing_expr"],
	); err != nil {
		return nil, err
	}
	return documents, nil
}

func pricingDocumentValues(documents *PricingDocuments) (map[string]string, error) {
	values := make(map[string]string, len(modelPricingOptionKeys))
	for _, key := range modelPricingNumericOptionKeys {
		value, err := common.Marshal(documents.Numeric[key])
		if err != nil {
			return nil, fmt.Errorf("serialize pricing option %q: %w", key, err)
		}
		values[key] = string(value)
	}
	for _, key := range modelPricingStringOptionKeys {
		value, err := common.Marshal(documents.Strings[key])
		if err != nil {
			return nil, fmt.Errorf("serialize pricing option %q: %w", key, err)
		}
		values[key] = string(value)
	}
	resolutionValue, err := common.Marshal(documents.ResolutionPrice)
	if err != nil {
		return nil, fmt.Errorf("serialize %s: %w", ratio_setting.VideoResolutionPriceOptionKey, err)
	}
	values[ratio_setting.VideoResolutionPriceOptionKey] = string(resolutionValue)
	if _, err := parsePricingDocuments(values); err != nil {
		return nil, err
	}
	return values, nil
}

func clonePricingValues(values map[string]string) map[string]string {
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func writeAllPricingDocuments(tx *gorm.DB, values map[string]string) error {
	for _, key := range modelPricingOptionKeys {
		if err := tx.Model(&Option{}).Where(&Option{Key: key}).Update("value", values[key]).Error; err != nil {
			return fmt.Errorf("write pricing option %q: %w", key, err)
		}
	}
	return nil
}

func loadCommittedPricingDocuments() (map[string]string, error) {
	var values map[string]string
	err := DB.Transaction(func(tx *gorm.DB) error {
		documents, err := lockPricingDocuments(tx)
		if err != nil {
			return err
		}
		values = clonePricingValues(documents.Raw)
		return nil
	})
	return values, err
}

func executePricingTransaction(mutation pricingTransactionMutation) (ModelPricingCommandResult, error) {
	return executePricingTransactionWithPrelock(nil, mutation)
}

func executePricingTransactionWithPrelock(prelock func(tx *gorm.DB) error, mutation pricingTransactionMutation) (ModelPricingCommandResult, error) {
	modelPricingOptionMu.Lock()
	defer modelPricingOptionMu.Unlock()

	result := ModelPricingCommandResult{}
	var committedValues map[string]string
	err := DB.Transaction(func(tx *gorm.DB) error {
		if prelock != nil {
			if err := prelock(tx); err != nil {
				return err
			}
		}
		documents, err := lockPricingDocuments(tx)
		if err != nil {
			return err
		}
		values, err := mutation(tx, documents)
		if err != nil {
			return err
		}
		committedValues = clonePricingValues(values)
		return nil
	})
	if err != nil {
		return result, err
	}

	result.Committed = true
	result.Values = clonePricingValues(committedValues)
	if err := publishPricingDocumentsAfterCommit(committedValues); err == nil {
		return result, nil
	} else {
		common.SysError("pricing documents committed but initial publication failed: " + err.Error())
	}
	InvalidatePricingCache()
	ratio_setting.InvalidateExposedDataCache()

	reloaded, reloadErr := loadCommittedPricingDocuments()
	if reloadErr == nil {
		if publishErr := publishPricingDocumentsLowLevel(reloaded); publishErr == nil {
			result.PublicationRecovered = true
			result.Values = clonePricingValues(reloaded)
			return result, nil
		} else {
			common.SysError("pricing publication recovery failed: " + publishErr.Error())
		}
	} else {
		common.SysError("pricing publication recovery reload failed: " + reloadErr.Error())
	}

	InvalidatePricingCache()
	ratio_setting.InvalidateExposedDataCache()
	result.PublicationPending = true
	return result, nil
}

func publishPricingDocumentsWithHooks(values map[string]string) error {
	return publishPricingDocuments(values, publishVideoResolutionPriceOption)
}

func publishPricingDocumentsLowLevel(values map[string]string) error {
	return publishPricingDocuments(values, publishVideoResolutionPriceOptionLowLevel)
}

type pricingPublicationStep struct {
	Key   string
	Value string
}

func mergePricingDocumentValues(current, final string) (string, error) {
	currentValues := make(map[string]json.RawMessage)
	if err := common.Unmarshal([]byte(current), &currentValues); err != nil {
		return "", err
	}
	finalValues := make(map[string]json.RawMessage)
	if err := common.Unmarshal([]byte(final), &finalValues); err != nil {
		return "", err
	}
	for key, value := range finalValues {
		currentValues[key] = value
	}
	merged, err := common.Marshal(currentValues)
	if err != nil {
		return "", err
	}
	return string(merged), nil
}

func pricingPublicationSteps(current, final map[string]string) ([]pricingPublicationStep, error) {
	// Publication is necessarily multi-map, so stage both base pricing modes and
	// expressions before switching activation documents. This prevents readers
	// that do not share the writer mutex from observing an unpriced model or a
	// tiered mode without its expression. Final cleanup happens only after the
	// replacement mode is live; resolution pricing remains the final publish.
	stagedPrice, err := mergePricingDocumentValues(current["ModelPrice"], final["ModelPrice"])
	if err != nil {
		return nil, fmt.Errorf("stage ModelPrice publication: %w", err)
	}
	stagedRatio, err := mergePricingDocumentValues(current["ModelRatio"], final["ModelRatio"])
	if err != nil {
		return nil, fmt.Errorf("stage ModelRatio publication: %w", err)
	}
	stagedExpression, err := mergePricingDocumentValues(
		current["billing_setting.billing_expr"],
		final["billing_setting.billing_expr"],
	)
	if err != nil {
		return nil, fmt.Errorf("stage billing expressions publication: %w", err)
	}

	return []pricingPublicationStep{
		{Key: "ModelPrice", Value: stagedPrice},
		{Key: "ModelRatio", Value: stagedRatio},
		{Key: "AudioCompletionRatio", Value: final["AudioCompletionRatio"]},
		{Key: "AudioRatio", Value: final["AudioRatio"]},
		{Key: "CacheRatio", Value: final["CacheRatio"]},
		{Key: "CompletionRatio", Value: final["CompletionRatio"]},
		{Key: "CreateCacheRatio", Value: final["CreateCacheRatio"]},
		{Key: "ImageRatio", Value: final["ImageRatio"]},
		{Key: "TaskBillingMode", Value: final["TaskBillingMode"]},
		{Key: "billing_setting.billing_expr", Value: stagedExpression},
		{Key: "billing_setting.billing_mode", Value: final["billing_setting.billing_mode"]},
		{Key: "billing_setting.billing_expr", Value: final["billing_setting.billing_expr"]},
		{Key: "ModelRatio", Value: final["ModelRatio"]},
		{Key: "ModelPrice", Value: final["ModelPrice"]},
		{Key: ratio_setting.VideoResolutionPriceOptionKey, Value: final[ratio_setting.VideoResolutionPriceOptionKey]},
	}, nil
}

func publishPricingDocuments(values map[string]string, videoPublisher func(string) error) error {
	if _, err := parsePricingDocuments(values); err != nil {
		return err
	}
	current := make(map[string]string, len(modelPricingOptionKeys))
	for _, key := range modelPricingOptionKeys {
		current[key] = currentPricingOptionValue(key)
	}
	steps, err := pricingPublicationSteps(current, values)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if step.Key == ratio_setting.VideoResolutionPriceOptionKey {
			if err := videoPublisher(step.Value); err != nil {
				return fmt.Errorf("publish %s: %w", step.Key, err)
			}
			continue
		}
		if err := publishPricingOptionLowLevel(step.Key, step.Value); err != nil {
			return fmt.Errorf("publish pricing option %q: %w", step.Key, err)
		}
	}
	InvalidatePricingCache()
	ratio_setting.InvalidateExposedDataCache()
	return nil
}

func publishPricingOptionLowLevel(key, value string) error {
	return updateOptionMap(key, value)
}

func publishVideoResolutionPriceOptionLowLevel(value string) error {
	if err := ratio_setting.ValidateVideoResolutionPriceByJSONString(value); err != nil {
		return err
	}
	if err := ratio_setting.UpdateVideoResolutionPriceByJSONString(value); err != nil {
		return err
	}
	common.OptionMapRWMutex.Lock()
	common.OptionMap[ratio_setting.VideoResolutionPriceOptionKey] = value
	common.OptionMapRWMutex.Unlock()
	InvalidatePricingCache()
	return nil
}

func deletePricingName(documents *PricingDocuments, name string) {
	for _, document := range documents.Numeric {
		delete(document, name)
	}
	for _, document := range documents.Strings {
		delete(document, name)
	}
	delete(documents.ResolutionPrice, name)
}

func renamePricingName(documents *PricingDocuments, source, target string) {
	for _, document := range documents.Numeric {
		if value, ok := document[source]; ok {
			delete(document, source)
			document[target] = value
		}
	}
	for _, document := range documents.Strings {
		if value, ok := document[source]; ok {
			delete(document, source)
			document[target] = value
		}
	}
	if value, ok := documents.ResolutionPrice[source]; ok {
		delete(documents.ResolutionPrice, source)
		documents.ResolutionPrice[target] = value
	}
}

func copyPricingName(documents *PricingDocuments, source, target string) {
	for _, document := range documents.Numeric {
		if value, ok := document[source]; ok {
			document[target] = value
		} else {
			delete(document, target)
		}
	}
	for _, document := range documents.Strings {
		if value, ok := document[source]; ok {
			document[target] = value
		} else {
			delete(document, target)
		}
	}
	if value, ok := documents.ResolutionPrice[source]; ok {
		clone := make(map[string]float64, len(value))
		for resolution, price := range value {
			clone[resolution] = price
		}
		documents.ResolutionPrice[target] = clone
	} else {
		delete(documents.ResolutionPrice, target)
	}
}

func validateSelectionNumber(field string, value *float64) error {
	if value == nil {
		return nil
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) {
		return fmt.Errorf("%s must be finite", field)
	}
	return nil
}

func applyPricingSelection(documents *PricingDocuments, target string, selection *ModelPricingSelection) error {
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("target model name is required")
	}
	if selection == nil {
		return fmt.Errorf("pricing selection is required")
	}
	for field, value := range map[string]*float64{
		"price":                  selection.ModelPrice,
		"ratio":                  selection.ModelRatio,
		"cache_ratio":            selection.CacheRatio,
		"create_cache_ratio":     selection.CreateCacheRatio,
		"completion_ratio":       selection.CompletionRatio,
		"image_ratio":            selection.ImageRatio,
		"audio_ratio":            selection.AudioRatio,
		"audio_completion_ratio": selection.AudioCompletionRatio,
	} {
		if err := validateSelectionNumber(field, value); err != nil {
			return err
		}
	}

	if selection.Mode == PricingModeVideoResolution {
		if len(selection.ResolutionPrices) == 0 {
			return fmt.Errorf("video resolution pricing requires at least one resolution")
		}
		prices := make(map[string]float64, len(selection.ResolutionPrices))
		for resolution, price := range selection.ResolutionPrices {
			prices[resolution] = price
		}
		documents.ResolutionPrice[target] = prices
		return nil
	}

	deletePricingName(documents, target)
	setNumber := func(key string, value *float64) {
		if value != nil {
			documents.Numeric[key][target] = *value
		}
	}
	switch selection.Mode {
	case PricingModeFixed:
		if selection.ModelPrice == nil {
			return fmt.Errorf("fixed pricing requires price")
		}
		setNumber("ModelPrice", selection.ModelPrice)
		if selection.TaskBillingMode != nil {
			documents.Strings["TaskBillingMode"][target] = *selection.TaskBillingMode
		}
	case PricingModeRatio:
		setNumber("ModelRatio", selection.ModelRatio)
		setNumber("CacheRatio", selection.CacheRatio)
		setNumber("CreateCacheRatio", selection.CreateCacheRatio)
		setNumber("CompletionRatio", selection.CompletionRatio)
		setNumber("ImageRatio", selection.ImageRatio)
		setNumber("AudioRatio", selection.AudioRatio)
		setNumber("AudioCompletionRatio", selection.AudioCompletionRatio)
	case PricingModeExpression:
		if selection.BillingExpr == nil || strings.TrimSpace(*selection.BillingExpr) == "" {
			return fmt.Errorf("expression pricing requires billing expression")
		}
		documents.Strings["billing_setting.billing_mode"][target] = billing_setting.BillingModeTieredExpr
		documents.Strings["billing_setting.billing_expr"][target] = *selection.BillingExpr
		setNumber("ModelPrice", selection.ModelPrice)
		setNumber("ModelRatio", selection.ModelRatio)
		setNumber("CacheRatio", selection.CacheRatio)
		setNumber("CreateCacheRatio", selection.CreateCacheRatio)
		setNumber("CompletionRatio", selection.CompletionRatio)
		setNumber("ImageRatio", selection.ImageRatio)
		setNumber("AudioRatio", selection.AudioRatio)
		setNumber("AudioCompletionRatio", selection.AudioCompletionRatio)
	default:
		return fmt.Errorf("unsupported pricing mode %q", selection.Mode)
	}
	return nil
}

func modelMutationID(mutation *ModelRowMutation) int {
	if mutation == nil {
		return 0
	}
	if mutation.ID != 0 {
		return mutation.ID
	}
	if mutation.Model != nil {
		return mutation.Model.Id
	}
	return 0
}

func lockModelRowForMutation(tx *gorm.DB, mutation *ModelRowMutation) (*Model, error) {
	if mutation == nil || mutation.Kind == "create" {
		return nil, nil
	}
	if mutation.Kind != "update" && mutation.Kind != "delete" {
		return nil, fmt.Errorf("unsupported model mutation %q", mutation.Kind)
	}
	id := modelMutationID(mutation)
	if id == 0 {
		return nil, fmt.Errorf("%s model mutation requires id", mutation.Kind)
	}
	var current Model
	if err := lockForUpdate(tx).Where("id = ?", id).First(&current).Error; err != nil {
		return nil, err
	}
	return &current, nil
}

func validateModelMutationCoupling(command ModelPricingCommand, current *Model) error {
	mutation := command.ModelMutation
	if mutation == nil {
		return nil
	}
	newName := ""
	if mutation.Model != nil {
		newName = mutation.Model.ModelName
	}
	requireNewName := func() error {
		if mutation.Model == nil {
			return fmt.Errorf("%s model mutation requires model", mutation.Kind)
		}
		if newName != command.TargetName {
			return fmt.Errorf("model mutation target %q does not match pricing target %q", newName, command.TargetName)
		}
		return nil
	}
	requireCurrentName := func(expected string) error {
		if current == nil {
			return fmt.Errorf("%s model mutation requires locked model row", mutation.Kind)
		}
		if current.ModelName != expected {
			return fmt.Errorf("model mutation row %q does not match pricing model %q", current.ModelName, expected)
		}
		return nil
	}

	switch command.Kind {
	case PricingCommandSave:
		if mutation.Kind != "create" && mutation.Kind != "update" {
			return fmt.Errorf("save pricing command does not support model mutation %q", mutation.Kind)
		}
		if err := requireNewName(); err != nil {
			return err
		}
		if mutation.Kind == "update" {
			return requireCurrentName(command.TargetName)
		}
	case PricingCommandRename:
		if mutation.Kind != "update" {
			return fmt.Errorf("rename pricing command requires update model mutation")
		}
		if err := requireCurrentName(command.SourceName); err != nil {
			return err
		}
		return requireNewName()
	case PricingCommandCopy:
		if mutation.Kind != "create" && mutation.Kind != "update" {
			return fmt.Errorf("copy pricing command does not support model mutation %q", mutation.Kind)
		}
		if err := requireNewName(); err != nil {
			return err
		}
		if mutation.Kind == "update" {
			return requireCurrentName(command.TargetName)
		}
	case PricingCommandDelete:
		if mutation.Kind != "delete" {
			return fmt.Errorf("delete pricing command requires delete model mutation")
		}
		return requireCurrentName(command.TargetName)
	case PricingCommandReplaceDocuments:
		return fmt.Errorf("replace-documents command cannot mutate a model row")
	default:
		return fmt.Errorf("unsupported pricing command %q", command.Kind)
	}
	return nil
}

func applyModelRowMutation(tx *gorm.DB, mutation *ModelRowMutation, current *Model) error {
	if mutation == nil {
		return nil
	}
	switch mutation.Kind {
	case "create":
		if mutation.Model == nil {
			return fmt.Errorf("create model mutation requires model")
		}
		now := common.GetTimestamp()
		mutation.Model.CreatedTime = now
		mutation.Model.UpdatedTime = now
		status := mutation.Model.Status
		syncOfficial := mutation.Model.SyncOfficial
		if err := tx.Create(mutation.Model).Error; err != nil {
			return err
		}
		return tx.Model(&Model{}).Where("id = ?", mutation.Model.Id).Updates(map[string]any{
			"status": status, "sync_official": syncOfficial,
		}).Error
	case "update":
		if mutation.Model == nil {
			return fmt.Errorf("update model mutation requires model")
		}
		if current == nil {
			return fmt.Errorf("update model mutation requires locked model row")
		}
		id := current.Id
		mutation.Model.Id = id
		mutation.Model.UpdatedTime = common.GetTimestamp()
		return tx.Model(&Model{}).Where("id = ?", id).
			Select("model_name", "description", "icon", "tags", "vendor_id", "endpoints", "status", "sync_official", "name_rule", "updated_time").
			Updates(mutation.Model).Error
	case "delete":
		if current == nil {
			return fmt.Errorf("delete model mutation requires locked model row")
		}
		return tx.Delete(current).Error
	default:
		return fmt.Errorf("unsupported model mutation %q", mutation.Kind)
	}
}

func ExecuteModelPricingCommand(command ModelPricingCommand) (ModelPricingCommandResult, error) {
	var lockedModel *Model
	prelock := func(tx *gorm.DB) error {
		var err error
		lockedModel, err = lockModelRowForMutation(tx, command.ModelMutation)
		if err != nil {
			return err
		}
		return validateModelMutationCoupling(command, lockedModel)
	}
	return executePricingTransactionWithPrelock(prelock, func(tx *gorm.DB, documents *PricingDocuments) (map[string]string, error) {
		switch command.Kind {
		case PricingCommandSave:
			if err := applyPricingSelection(documents, command.TargetName, command.Selection); err != nil {
				return nil, err
			}
		case PricingCommandRename:
			if strings.TrimSpace(command.SourceName) == "" || strings.TrimSpace(command.TargetName) == "" {
				return nil, fmt.Errorf("source and target model names are required")
			}
			renamePricingName(documents, command.SourceName, command.TargetName)
			if command.Selection != nil {
				if err := applyPricingSelection(documents, command.TargetName, command.Selection); err != nil {
					return nil, err
				}
			}
		case PricingCommandCopy:
			if strings.TrimSpace(command.SourceName) == "" || strings.TrimSpace(command.TargetName) == "" {
				return nil, fmt.Errorf("source and target model names are required")
			}
			copyPricingName(documents, command.SourceName, command.TargetName)
			if command.Selection != nil {
				if err := applyPricingSelection(documents, command.TargetName, command.Selection); err != nil {
					return nil, err
				}
			}
		case PricingCommandDelete:
			if strings.TrimSpace(command.TargetName) == "" {
				return nil, fmt.Errorf("target model name is required")
			}
			deletePricingName(documents, command.TargetName)
		case PricingCommandReplaceDocuments:
			values, err := replacePricingDocuments(tx, documents, command.Values, command.ExpectedDocuments)
			if err != nil {
				return nil, err
			}
			if err := applyModelRowMutation(tx, command.ModelMutation, lockedModel); err != nil {
				return nil, err
			}
			return values, nil
		default:
			return nil, fmt.Errorf("unsupported pricing command %q", command.Kind)
		}

		values, err := pricingDocumentValues(documents)
		if err != nil {
			return nil, err
		}
		if err := writeAllPricingDocuments(tx, values); err != nil {
			return nil, err
		}
		if err := applyModelRowMutation(tx, command.ModelMutation, lockedModel); err != nil {
			return nil, err
		}
		return values, nil
	})
}

func replacePricingDocuments(tx *gorm.DB, documents *PricingDocuments, values, expected map[string]string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("replacement values are required")
	}
	finalValues := clonePricingValues(documents.Raw)
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if _, ok := modelPricingOptionKeySet[key]; !ok {
			return nil, fmt.Errorf("option %q is not a protected pricing document", key)
		}
		if expected != nil && documents.Raw[key] != expected[key] {
			return nil, &OptionConflictError{Key: key, CurrentValue: documents.Raw[key]}
		}
		finalValues[key] = value
		keys = append(keys, key)
	}
	if _, err := parsePricingDocuments(finalValues); err != nil {
		return nil, err
	}
	sort.Strings(keys)
	for _, key := range keys {
		if expected == nil {
			if err := tx.Model(&Option{}).Where(&Option{Key: key}).Update("value", finalValues[key]).Error; err != nil {
				return nil, err
			}
			continue
		}
		if finalValues[key] == expected[key] {
			continue
		}
		update := tx.Model(&Option{}).
			Where(&Option{Key: key, Value: expected[key]}).
			Update("value", finalValues[key])
		if update.Error != nil {
			return nil, update.Error
		}
		if update.RowsAffected != 1 {
			var current Option
			if err := tx.Where(&Option{Key: key}).First(&current).Error; err != nil {
				return nil, err
			}
			return nil, &OptionConflictError{Key: key, CurrentValue: current.Value}
		}
	}
	return finalValues, nil
}

func UpdateOptionCAS(key, value, expected string) error {
	return UpdateOptionsBulkCAS(map[string]string{key: value}, map[string]string{key: expected})
}

func UpdateOptionsBulkCAS(values, expected map[string]string) error {
	_, err := executePricingTransaction(func(tx *gorm.DB, documents *PricingDocuments) (map[string]string, error) {
		return replacePricingDocuments(tx, documents, values, expected)
	})
	return err
}

func isProtectedPricingOption(key string) bool {
	_, ok := modelPricingOptionKeySet[key]
	return ok
}

func updateProtectedPricingOptions(values map[string]string) (ModelPricingCommandResult, error) {
	return executePricingTransaction(func(tx *gorm.DB, documents *PricingDocuments) (map[string]string, error) {
		return replacePricingDocuments(tx, documents, values, nil)
	})
}

func updateOptionsIncludingProtected(values map[string]string) (ModelPricingCommandResult, error) {
	return executePricingTransaction(func(tx *gorm.DB, documents *PricingDocuments) (map[string]string, error) {
		protectedValues := make(map[string]string)
		unprotectedKeys := make([]string, 0, len(values))
		for key, value := range values {
			if isProtectedPricingOption(key) {
				protectedValues[key] = value
			} else {
				unprotectedKeys = append(unprotectedKeys, key)
			}
		}
		finalValues := clonePricingValues(documents.Raw)
		for key, value := range protectedValues {
			finalValues[key] = value
		}
		if _, err := parsePricingDocuments(finalValues); err != nil {
			return nil, err
		}
		if err := writeAllPricingDocuments(tx, finalValues); err != nil {
			return nil, err
		}
		sort.Strings(unprotectedKeys)
		for _, key := range unprotectedKeys {
			option := Option{Key: key}
			if err := tx.FirstOrCreate(&option, Option{Key: key}).Error; err != nil {
				return nil, err
			}
			option.Value = values[key]
			if err := tx.Save(&option).Error; err != nil {
				return nil, err
			}
		}
		return finalValues, nil
	})
}
