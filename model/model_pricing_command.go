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
	InvalidNumeric  map[string]map[string]json.RawMessage
	Strings         map[string]map[string]string
	ResolutionPrice map[string]map[string]float64
	Raw             map[string]string
	// InvalidRawDocuments 是文档级隔离区（仅锁定/加载路径产生）：老构建落库的
	// 整份坏文档保留原始字节、不进语义映射。写入侧的终值严格校验负责阻止它们
	// 未经修复地随语义命令重写；replace_documents 覆盖它们即完成修复。
	InvalidRawDocuments map[string]string
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

type PricingValidationError struct {
	Err error
}

func (e *PricingValidationError) Error() string {
	return e.Err.Error()
}

func (e *PricingValidationError) Unwrap() error {
	return e.Err
}

func pricingValidationErrorf(format string, args ...any) error {
	return &PricingValidationError{Err: fmt.Errorf(format, args...)}
}

func pricingValidationError(err error) error {
	if err == nil {
		return nil
	}
	return &PricingValidationError{Err: err}
}

type pricingTransactionMutation func(tx *gorm.DB, documents *PricingDocuments) (map[string]string, error)

// publishPricingDocumentsAfterCommit is the failpoint-bearing publication
// boundary. Recovery deliberately bypasses it and calls the validated
// low-level publisher directly.
var publishPricingDocumentsAfterCommit = publishPricingDocumentsWithHooks

func currentPricingOptionDefaults() map[string]string {
	values := make(map[string]string, len(modelPricingOptionKeys))
	common.OptionMapRWMutex.RLock()
	for _, key := range modelPricingOptionKeys {
		if value := common.OptionMap[key]; value != "" {
			values[key] = value
		}
	}
	common.OptionMapRWMutex.RUnlock()
	billingModes, billingExpressions := billing_setting.PricingDocumentsJSON()
	values["billing_setting.billing_mode"] = billingModes
	values["billing_setting.billing_expr"] = billingExpressions

	for _, key := range modelPricingOptionKeys {
		if _, ok := values[key]; ok {
			continue
		}
		switch key {
		case "AudioCompletionRatio":
			values[key] = ratio_setting.AudioCompletionRatio2JSONString()
		case "AudioRatio":
			values[key] = ratio_setting.AudioRatio2JSONString()
		case "CacheRatio":
			values[key] = ratio_setting.CacheRatio2JSONString()
		case "CompletionRatio":
			values[key] = ratio_setting.CompletionRatio2JSONString()
		case "CreateCacheRatio":
			values[key] = ratio_setting.CreateCacheRatio2JSONString()
		case "ImageRatio":
			values[key] = ratio_setting.ImageRatio2JSONString()
		case "ModelPrice":
			values[key] = ratio_setting.ModelPrice2JSONString()
		case "ModelRatio":
			values[key] = ratio_setting.ModelRatio2JSONString()
		case "TaskBillingMode":
			values[key] = ratio_setting.TaskBillingMode2JSONString()
		case ratio_setting.VideoResolutionPriceOptionKey:
			values[key] = ratio_setting.VideoResolutionPrice2JSONString()
		default:
			values[key] = "{}"
		}
	}
	return values
}

func lockPricingDocuments(tx *gorm.DB) (*PricingDocuments, error) {
	values := make(map[string]string, len(modelPricingOptionKeys))
	defaults := currentPricingOptionDefaults()
	for _, key := range modelPricingOptionKeys {
		missing := Option{Key: key, Value: defaults[key]}
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
	return parseLockedPricingDocuments(values)
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
	return parsePricingDocumentsWithMode(values, true)
}

func parseLockedPricingDocuments(values map[string]string) (*PricingDocuments, error) {
	return parsePricingDocumentsWithMode(values, false)
}

func parsePricingDocumentsWithMode(values map[string]string, strict bool) (*PricingDocuments, error) {
	documents := &PricingDocuments{
		Numeric:             make(map[string]map[string]float64, len(modelPricingNumericOptionKeys)),
		InvalidNumeric:      make(map[string]map[string]json.RawMessage, len(modelPricingNumericOptionKeys)),
		Strings:             make(map[string]map[string]string, len(modelPricingStringOptionKeys)),
		ResolutionPrice:     make(map[string]map[string]float64),
		Raw:                 make(map[string]string, len(modelPricingOptionKeys)),
		InvalidRawDocuments: make(map[string]string),
	}
	// 严格模式（写入侧）任何失败都致命；宽松模式（锁定/加载侧）把整份坏文档
	// 隔离起来，避免一份老构建落库的脏文档阻塞其余十一份的发布与修复。
	quarantine := func(key string, err error) error {
		if strict {
			return err
		}
		documents.InvalidRawDocuments[key] = values[key]
		return nil
	}
	for _, key := range modelPricingOptionKeys {
		value, ok := values[key]
		if !ok {
			return nil, fmt.Errorf("missing pricing option %q", key)
		}
		documents.Raw[key] = value
		if err := requireJSONObject(key, value); err != nil {
			if err := quarantine(key, err); err != nil {
				return nil, err
			}
		}
	}
	for _, key := range modelPricingNumericOptionKeys {
		documents.Numeric[key] = make(map[string]float64)
		documents.InvalidNumeric[key] = make(map[string]json.RawMessage)
		if _, bad := documents.InvalidRawDocuments[key]; bad {
			continue
		}
		var rawDocument map[string]json.RawMessage
		if err := common.Unmarshal([]byte(values[key]), &rawDocument); err != nil {
			if err := quarantine(key, fmt.Errorf("parse pricing option %q: %w", key, err)); err != nil {
				return nil, err
			}
			continue
		}
		document := make(map[string]float64, len(rawDocument))
		invalidDocument := make(map[string]json.RawMessage)
		var documentErr error
		for model, rawValue := range rawDocument {
			if strings.TrimSpace(model) == "" {
				documentErr = fmt.Errorf("pricing option %q contains a blank model key", key)
				break
			}
			if common.GetJsonType(rawValue) != "number" {
				if strict {
					documentErr = fmt.Errorf("pricing option %q contains a non-number value for model %q", key, model)
					break
				}
				invalidDocument[model] = rawValue
				continue
			}
			var value float64
			if err := common.Unmarshal(rawValue, &value); err != nil {
				if strict {
					documentErr = fmt.Errorf("parse pricing option %q value for model %q: %w", key, model, err)
					break
				}
				invalidDocument[model] = rawValue
				continue
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				if strict {
					documentErr = fmt.Errorf("pricing option %q contains a non-finite value for model %q", key, model)
					break
				}
				invalidDocument[model] = rawValue
				continue
			}
			if value < 0 {
				if strict {
					documentErr = fmt.Errorf("pricing option %q contains a negative value for model %q", key, model)
					break
				}
				invalidDocument[model] = rawValue
				continue
			}
			document[model] = value
		}
		if documentErr != nil {
			if err := quarantine(key, documentErr); err != nil {
				return nil, err
			}
			continue
		}
		documents.Numeric[key] = document
		documents.InvalidNumeric[key] = invalidDocument
	}
	for _, key := range modelPricingStringOptionKeys {
		documents.Strings[key] = make(map[string]string)
		if _, bad := documents.InvalidRawDocuments[key]; bad {
			continue
		}
		var document map[string]string
		if err := common.Unmarshal([]byte(values[key]), &document); err != nil {
			if err := quarantine(key, fmt.Errorf("parse pricing option %q: %w", key, err)); err != nil {
				return nil, err
			}
			continue
		}
		if document == nil {
			document = make(map[string]string)
		}
		var blankErr error
		for model := range document {
			if strings.TrimSpace(model) == "" {
				blankErr = fmt.Errorf("pricing option %q contains a blank model key", key)
				break
			}
		}
		if blankErr != nil {
			if err := quarantine(key, blankErr); err != nil {
				return nil, err
			}
			continue
		}
		documents.Strings[key] = document
	}
	if _, bad := documents.InvalidRawDocuments[ratio_setting.VideoResolutionPriceOptionKey]; !bad {
		if err := ratio_setting.ValidateVideoResolutionPriceByJSONString(values[ratio_setting.VideoResolutionPriceOptionKey]); err != nil {
			if err := quarantine(ratio_setting.VideoResolutionPriceOptionKey,
				fmt.Errorf("invalid %s: %w", ratio_setting.VideoResolutionPriceOptionKey, err)); err != nil {
				return nil, err
			}
		} else if err := common.Unmarshal([]byte(values[ratio_setting.VideoResolutionPriceOptionKey]), &documents.ResolutionPrice); err != nil {
			if err := quarantine(ratio_setting.VideoResolutionPriceOptionKey,
				fmt.Errorf("parse %s: %w", ratio_setting.VideoResolutionPriceOptionKey, err)); err != nil {
				return nil, err
			}
		}
	}
	if documents.ResolutionPrice == nil {
		documents.ResolutionPrice = make(map[string]map[string]float64)
	}
	// billing mode 与 expression 成对约束：任一侧坏则两者一起隔离，
	// 防止发布出没有表达式的 tiered 模式。
	const billingModeKey = "billing_setting.billing_mode"
	const billingExprKey = "billing_setting.billing_expr"
	_, badMode := documents.InvalidRawDocuments[billingModeKey]
	_, badExpr := documents.InvalidRawDocuments[billingExprKey]
	if !badMode && !badExpr {
		if err := billing_setting.ValidatePricingDocuments(values[billingModeKey], values[billingExprKey]); err != nil {
			if strict {
				return nil, err
			}
			badMode = true
		}
	}
	if badMode || badExpr {
		documents.InvalidRawDocuments[billingModeKey] = values[billingModeKey]
		documents.InvalidRawDocuments[billingExprKey] = values[billingExprKey]
		documents.Strings[billingModeKey] = make(map[string]string)
		documents.Strings[billingExprKey] = make(map[string]string)
	}
	return documents, nil
}

func pricingDocumentValues(documents *PricingDocuments) (map[string]string, error) {
	values := make(map[string]string, len(modelPricingOptionKeys))
	for _, key := range modelPricingNumericOptionKeys {
		if rawValue, bad := documents.InvalidRawDocuments[key]; bad {
			values[key] = rawValue
			continue
		}
		rawDocument := make(map[string]json.RawMessage, len(documents.Numeric[key])+len(documents.InvalidNumeric[key]))
		for model, rawValue := range documents.InvalidNumeric[key] {
			rawDocument[model] = rawValue
		}
		for model, numericValue := range documents.Numeric[key] {
			rawValue, err := common.Marshal(numericValue)
			if err != nil {
				return nil, fmt.Errorf("serialize pricing option %q value for model %q: %w", key, model, err)
			}
			rawDocument[model] = rawValue
		}
		value, err := common.Marshal(rawDocument)
		if err != nil {
			return nil, fmt.Errorf("serialize pricing option %q: %w", key, err)
		}
		values[key] = string(value)
	}
	for _, key := range modelPricingStringOptionKeys {
		if rawValue, bad := documents.InvalidRawDocuments[key]; bad {
			values[key] = rawValue
			continue
		}
		value, err := common.Marshal(documents.Strings[key])
		if err != nil {
			return nil, fmt.Errorf("serialize pricing option %q: %w", key, err)
		}
		values[key] = string(value)
	}
	if rawValue, bad := documents.InvalidRawDocuments[ratio_setting.VideoResolutionPriceOptionKey]; bad {
		values[ratio_setting.VideoResolutionPriceOptionKey] = rawValue
	} else {
		resolutionValue, err := common.Marshal(documents.ResolutionPrice)
		if err != nil {
			return nil, fmt.Errorf("serialize %s: %w", ratio_setting.VideoResolutionPriceOptionKey, err)
		}
		values[ratio_setting.VideoResolutionPriceOptionKey] = string(resolutionValue)
	}
	// 终值严格校验：隔离区文档按原始字节透传，因此任何未经本次命令修复的
	// 文档级坏数据都会在这里被归类为客户端验证错误，绝不悄悄重写。
	if _, err := parsePricingDocuments(values); err != nil {
		return nil, pricingValidationError(err)
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

// loadPublishablePricingDocuments 服务于启动/同步加载：整套定价绝不因一份
// 老构建落库的坏文档而静默滞留在编译默认值。坏文档单独回退到当前已发布状态
// （冷启动即内置默认），其余文档照常发布；降级的键返回给调用方响亮告警。
func loadPublishablePricingDocuments() (map[string]string, []string, error) {
	var values map[string]string
	var degraded []string
	err := DB.Transaction(func(tx *gorm.DB) error {
		documents, err := lockPricingDocuments(tx)
		if err != nil {
			return err
		}
		values = clonePricingValues(documents.Raw)
		if len(documents.InvalidRawDocuments) == 0 {
			return nil
		}
		fallbacks := currentPricingOptionDefaults()
		for _, key := range modelPricingOptionKeys {
			if _, bad := documents.InvalidRawDocuments[key]; bad {
				values[key] = fallbacks[key]
				degraded = append(degraded, key)
			}
		}
		return nil
	})
	return values, degraded, err
}

func executePricingTransaction(mutation pricingTransactionMutation) (ModelPricingCommandResult, error) {
	return executePricingTransactionWithPrelock(nil, mutation)
}

func executePricingTransactionWithPrelock(prelock func(tx *gorm.DB) error, mutation pricingTransactionMutation) (ModelPricingCommandResult, error) {
	modelPricingOptionMu.Lock()
	defer modelPricingOptionMu.Unlock()

	result := ModelPricingCommandResult{}
	var committedValues map[string]string
	err := modelNamespaceTransaction(DB, func(tx *gorm.DB) error {
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
	Key         string
	Value       string
	BillingMode string
	BillingExpr string
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
		{
			BillingMode: current["billing_setting.billing_mode"],
			BillingExpr: stagedExpression,
		},
		{
			BillingMode: final["billing_setting.billing_mode"],
			BillingExpr: stagedExpression,
		},
		{
			BillingMode: final["billing_setting.billing_mode"],
			BillingExpr: final["billing_setting.billing_expr"],
		},
		{Key: "ModelRatio", Value: final["ModelRatio"]},
		{Key: "ModelPrice", Value: final["ModelPrice"]},
		{Key: ratio_setting.VideoResolutionPriceOptionKey, Value: final[ratio_setting.VideoResolutionPriceOptionKey]},
	}, nil
}

func publishPricingDocuments(values map[string]string, videoPublisher func(string) error) error {
	if _, err := parsePricingDocuments(values); err != nil {
		return err
	}
	current := currentPricingOptionDefaults()
	steps, err := pricingPublicationSteps(current, values)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if step.BillingMode != "" {
			if err := publishBillingPricingDocumentsLowLevel(step.BillingMode, step.BillingExpr); err != nil {
				return fmt.Errorf("publish billing pricing documents: %w", err)
			}
			continue
		}
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

func publishBillingPricingDocumentsLowLevel(modeValue, exprValue string) error {
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	if err := billing_setting.UpdatePricingDocuments(modeValue, exprValue); err != nil {
		return err
	}
	common.OptionMap["billing_setting.billing_mode"] = modeValue
	common.OptionMap["billing_setting.billing_expr"] = exprValue
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
	for _, document := range documents.InvalidNumeric {
		delete(document, name)
	}
	for _, document := range documents.Strings {
		delete(document, name)
	}
	delete(documents.ResolutionPrice, name)
}

func renamePricingName(documents *PricingDocuments, source, target string) {
	for key, document := range documents.Numeric {
		if value, ok := document[source]; ok {
			delete(document, source)
			delete(documents.InvalidNumeric[key], target)
			document[target] = value
		}
	}
	for key, document := range documents.InvalidNumeric {
		if value, ok := document[source]; ok {
			delete(document, source)
			delete(documents.Numeric[key], target)
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
	for key, document := range documents.Numeric {
		if value, ok := document[source]; ok {
			delete(documents.InvalidNumeric[key], target)
			document[target] = value
		} else if invalidValue, ok := documents.InvalidNumeric[key][source]; ok {
			delete(document, target)
			documents.InvalidNumeric[key][target] = invalidValue
		} else {
			delete(document, target)
			delete(documents.InvalidNumeric[key], target)
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
		return pricingValidationErrorf("%s must be finite", field)
	}
	if *value < 0 {
		return pricingValidationErrorf("%s must be non-negative", field)
	}
	return nil
}

func applyPricingSelection(documents *PricingDocuments, target string, selection *ModelPricingSelection) error {
	if strings.TrimSpace(target) == "" {
		return pricingValidationErrorf("target model name is required")
	}
	if selection == nil {
		return pricingValidationErrorf("pricing selection is required")
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
			return pricingValidationErrorf("video resolution pricing requires at least one resolution")
		}
		prices := make(map[string]float64, len(selection.ResolutionPrices))
		for resolution, price := range selection.ResolutionPrices {
			prices[resolution] = price
		}
		documents.ResolutionPrice[target] = prices
		return nil
	}

	switch selection.Mode {
	case PricingModeFixed:
		if selection.ModelPrice == nil {
			return pricingValidationErrorf("fixed pricing requires price")
		}
	case PricingModeRatio:
		if selection.ModelRatio == nil {
			return pricingValidationErrorf("per-token pricing requires ratio")
		}
	case PricingModeExpression:
		if selection.BillingExpr == nil || strings.TrimSpace(*selection.BillingExpr) == "" {
			return pricingValidationErrorf("expression pricing requires billing expression")
		}
	default:
		return pricingValidationErrorf("unsupported pricing mode %q", selection.Mode)
	}

	deletePricingName(documents, target)
	setNumber := func(key string, value *float64) {
		if value != nil {
			delete(documents.InvalidNumeric[key], target)
			documents.Numeric[key][target] = *value
		}
	}
	switch selection.Mode {
	case PricingModeFixed:
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
		return createModelRecord(tx, mutation.Model)
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
	var namePlan *modelNameMutationPlan
	if command.ModelMutation != nil {
		mutation := command.ModelMutation
		var (
			resolved modelNameMutationPlan
			err      error
		)
		switch mutation.Kind {
		case "create":
			if err = validateModelMutationCoupling(command, nil); err == nil {
				resolved, err = resolveModelNameMutation(DB, 0, nil, &mutation.Model.ModelName)
			}
		case "update", "delete":
			id := modelMutationID(mutation)
			if id == 0 {
				return ModelPricingCommandResult{}, fmt.Errorf("%s model mutation requires id", mutation.Kind)
			}
			expectedSourceName := &command.TargetName
			if command.Kind == PricingCommandRename {
				expectedSourceName = &command.SourceName
			}
			var targetName *string
			if mutation.Kind == "update" {
				targetName = &command.TargetName
			}
			resolved, err = resolveModelNameMutation(DB, id, expectedSourceName, targetName)
		default:
			err = fmt.Errorf("unsupported model mutation %q", mutation.Kind)
		}
		if err != nil {
			return ModelPricingCommandResult{}, err
		}
		namePlan = &resolved
	}

	var lockedModel *Model
	modelMutationApplied := false
	prelock := func(tx *gorm.DB) error {
		if command.ModelMutation == nil {
			return validateModelMutationCoupling(command, nil)
		}
		mutation := command.ModelMutation
		var err error
		lockedModel, err = lockModelNameMutation(tx, *namePlan)
		if err != nil {
			return err
		}
		switch mutation.Kind {
		case "create":
			if err := validateModelMutationCoupling(command, nil); err != nil {
				return err
			}
			if err := applyModelRowMutation(tx, mutation, nil); err != nil {
				return err
			}
			modelMutationApplied = true
		case "update", "delete":
			return validateModelMutationCoupling(command, lockedModel)
		}
		return nil
	}
	return executePricingTransactionWithPrelock(prelock, func(tx *gorm.DB, documents *PricingDocuments) (map[string]string, error) {
		switch command.Kind {
		case PricingCommandSave:
			if err := applyPricingSelection(documents, command.TargetName, command.Selection); err != nil {
				return nil, err
			}
		case PricingCommandRename:
			if strings.TrimSpace(command.SourceName) == "" || strings.TrimSpace(command.TargetName) == "" {
				return nil, pricingValidationErrorf("source and target model names are required")
			}
			renamePricingName(documents, command.SourceName, command.TargetName)
			if command.Selection != nil {
				if err := applyPricingSelection(documents, command.TargetName, command.Selection); err != nil {
					return nil, err
				}
			}
		case PricingCommandCopy:
			if strings.TrimSpace(command.SourceName) == "" || strings.TrimSpace(command.TargetName) == "" {
				return nil, pricingValidationErrorf("source and target model names are required")
			}
			copyPricingName(documents, command.SourceName, command.TargetName)
			if command.Selection != nil {
				if err := applyPricingSelection(documents, command.TargetName, command.Selection); err != nil {
					return nil, err
				}
			}
		case PricingCommandDelete:
			if strings.TrimSpace(command.TargetName) == "" {
				return nil, pricingValidationErrorf("target model name is required")
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
			return nil, pricingValidationErrorf("unsupported pricing command %q", command.Kind)
		}

		values, err := pricingDocumentValues(documents)
		if err != nil {
			return nil, err
		}
		if err := writeAllPricingDocuments(tx, values); err != nil {
			return nil, err
		}
		if !modelMutationApplied {
			if err := applyModelRowMutation(tx, command.ModelMutation, lockedModel); err != nil {
				return nil, err
			}
		}
		return values, nil
	})
}

func replacePricingDocuments(tx *gorm.DB, documents *PricingDocuments, values, expected map[string]string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, pricingValidationErrorf("replacement values are required")
	}
	finalValues := clonePricingValues(documents.Raw)
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if _, ok := modelPricingOptionKeySet[key]; !ok {
			return nil, pricingValidationErrorf("option %q is not a protected pricing document", key)
		}
		if expected != nil && documents.Raw[key] != expected[key] {
			return nil, &OptionConflictError{Key: key, CurrentValue: documents.Raw[key]}
		}
		finalValues[key] = value
		keys = append(keys, key)
	}
	if _, err := parsePricingDocuments(finalValues); err != nil {
		return nil, pricingValidationError(err)
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
