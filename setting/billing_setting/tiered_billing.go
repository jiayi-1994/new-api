package billing_setting

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/samber/lo"
)

const (
	BillingModeRatio      = "ratio"
	BillingModeTieredExpr = "tiered_expr"
	BillingModeField      = "billing_mode"
	BillingExprField      = "billing_expr"
)

// BillingSetting is managed by config.GlobalConfig.Register.
// DB keys: billing_setting.billing_mode, billing_setting.billing_expr
type BillingSetting struct {
	BillingMode map[string]string `json:"billing_mode"`
	BillingExpr map[string]string `json:"billing_expr"`
}

var billingSetting = BillingSetting{
	BillingMode: make(map[string]string),
	BillingExpr: make(map[string]string),
}
var billingSettingMu sync.RWMutex

func init() {
	config.GlobalConfig.Register("billing_setting", &billingSetting)
}

// ---------------------------------------------------------------------------
// Read accessors (hot path, must be fast)
// ---------------------------------------------------------------------------

func GetBillingMode(model string) string {
	billingSettingMu.RLock()
	defer billingSettingMu.RUnlock()
	if mode, ok := billingSetting.BillingMode[model]; ok {
		return mode
	}
	return BillingModeRatio
}

func GetBillingExpr(model string) (string, bool) {
	billingSettingMu.RLock()
	defer billingSettingMu.RUnlock()
	expr, ok := billingSetting.BillingExpr[model]
	return expr, ok
}

// GetBillingModeAndExpr reads both billing documents under one synchronization
// boundary so request paths cannot pair a new mode with an old expression.
func GetBillingModeAndExpr(model string) (string, string, bool) {
	billingSettingMu.RLock()
	defer billingSettingMu.RUnlock()
	mode, ok := billingSetting.BillingMode[model]
	if !ok {
		mode = BillingModeRatio
	}
	expr, hasExpr := billingSetting.BillingExpr[model]
	return mode, expr, hasExpr
}

func GetBillingModeCopy() map[string]string {
	billingSettingMu.RLock()
	defer billingSettingMu.RUnlock()
	return lo.Assign(billingSetting.BillingMode)
}

func GetBillingExprCopy() map[string]string {
	billingSettingMu.RLock()
	defer billingSettingMu.RUnlock()
	return lo.Assign(billingSetting.BillingExpr)
}

// PricingDocumentsJSON serializes the mode and expression maps from one
// in-memory snapshot.
func PricingDocumentsJSON() (string, string) {
	billingSettingMu.RLock()
	defer billingSettingMu.RUnlock()
	modes, modeErr := common.Marshal(billingSetting.BillingMode)
	expressions, expressionErr := common.Marshal(billingSetting.BillingExpr)
	if modeErr != nil || expressionErr != nil {
		return "{}", "{}"
	}
	return string(modes), string(expressions)
}

func BillingMode2JSONString() string {
	modes, _ := PricingDocumentsJSON()
	return modes
}

func BillingExpr2JSONString() string {
	_, expressions := PricingDocumentsJSON()
	return expressions
}

func GetPricingSyncData(base map[string]any) map[string]any {
	billingSettingMu.RLock()
	modes := lo.Assign(billingSetting.BillingMode)
	expressions := lo.Assign(billingSetting.BillingExpr)
	billingSettingMu.RUnlock()
	extra := make(map[string]any, 2)
	if len(modes) > 0 {
		extra[BillingModeField] = modes
	}
	if len(expressions) > 0 {
		extra[BillingExprField] = expressions
	}
	return lo.Assign(base, extra)
}

func decodePricingDocuments(modeValue, exprValue string) (map[string]string, map[string]string, error) {
	if err := ValidatePricingDocuments(modeValue, exprValue); err != nil {
		return nil, nil, err
	}
	var modes map[string]string
	if err := common.Unmarshal([]byte(modeValue), &modes); err != nil {
		return nil, nil, fmt.Errorf("parse billing modes: %w", err)
	}
	var expressions map[string]string
	if err := common.Unmarshal([]byte(exprValue), &expressions); err != nil {
		return nil, nil, fmt.Errorf("parse billing expressions: %w", err)
	}
	if modes == nil {
		modes = make(map[string]string)
	}
	if expressions == nil {
		expressions = make(map[string]string)
	}
	return modes, expressions, nil
}

// UpdatePricingDocuments validates both maps before atomically publishing the
// immutable pair to all readers.
func UpdatePricingDocuments(modeValue, exprValue string) error {
	modes, expressions, err := decodePricingDocuments(modeValue, exprValue)
	if err != nil {
		return err
	}
	billingSettingMu.Lock()
	billingSetting.BillingMode = modes
	billingSetting.BillingExpr = expressions
	billingSettingMu.Unlock()
	return nil
}

// UpdateConfigFromMap lets config.GlobalConfig route all reflected setters
// through the same billing snapshot lock. Partial updates validate against the
// current value while holding the writer lock, avoiding lost concurrent writes.
func (setting *BillingSetting) UpdateConfigFromMap(values map[string]string) error {
	billingSettingMu.Lock()
	defer billingSettingMu.Unlock()
	modeJSON, err := common.Marshal(setting.BillingMode)
	if err != nil {
		return err
	}
	exprJSON, err := common.Marshal(setting.BillingExpr)
	if err != nil {
		return err
	}
	if value, ok := values[BillingModeField]; ok {
		modeJSON = []byte(value)
	}
	if value, ok := values[BillingExprField]; ok {
		exprJSON = []byte(value)
	}
	modes, expressions, err := decodePricingDocuments(string(modeJSON), string(exprJSON))
	if err != nil {
		return err
	}
	setting.BillingMode = modes
	setting.BillingExpr = expressions
	return nil
}

// ConfigToMap lets config.GlobalConfig serialize the pair without racing a
// concurrent publisher.
func (setting *BillingSetting) ConfigToMap() (map[string]string, error) {
	billingSettingMu.RLock()
	defer billingSettingMu.RUnlock()
	modes, err := common.Marshal(setting.BillingMode)
	if err != nil {
		return nil, err
	}
	expressions, err := common.Marshal(setting.BillingExpr)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		BillingModeField: string(modes),
		BillingExprField: string(expressions),
	}, nil
}

// ---------------------------------------------------------------------------
// Smoke test (called externally for validation before save)
// ---------------------------------------------------------------------------

func SmokeTestExpr(exprStr string) error {
	return smokeTestExpr(exprStr)
}

// ValidatePricingDocuments validates the two persisted billing-setting maps
// without publishing either one. Callers can therefore validate an entire
// pricing snapshot before making any database or in-memory change.
func ValidatePricingDocuments(modeValue, exprValue string) error {
	if common.GetJsonType(json.RawMessage(strings.TrimSpace(modeValue))) != "object" {
		return fmt.Errorf("billing modes must be a JSON object")
	}
	if common.GetJsonType(json.RawMessage(strings.TrimSpace(exprValue))) != "object" {
		return fmt.Errorf("billing expressions must be a JSON object")
	}
	if err := common.ValidateJSONNoDuplicateKeys(json.RawMessage(modeValue)); err != nil {
		return fmt.Errorf("parse billing modes: %w", err)
	}
	if err := common.ValidateJSONNoDuplicateKeys(json.RawMessage(exprValue)); err != nil {
		return fmt.Errorf("parse billing expressions: %w", err)
	}

	var modes map[string]string
	if err := common.Unmarshal([]byte(modeValue), &modes); err != nil {
		return fmt.Errorf("parse billing modes: %w", err)
	}
	for model, mode := range modes {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("billing mode model key must not be blank")
		}
		if mode != BillingModeRatio && mode != BillingModeTieredExpr {
			return fmt.Errorf("unsupported billing mode %q for model %q", mode, model)
		}
	}

	var expressions map[string]string
	if err := common.Unmarshal([]byte(exprValue), &expressions); err != nil {
		return fmt.Errorf("parse billing expressions: %w", err)
	}
	for model, expression := range expressions {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("billing expression model key must not be blank")
		}
		if strings.TrimSpace(expression) == "" {
			return fmt.Errorf("billing expression for model %q must not be blank", model)
		}
		if err := smokeTestExpr(expression); err != nil {
			return fmt.Errorf("invalid billing expression for model %q: %w", model, err)
		}
	}
	for model, mode := range modes {
		if mode != BillingModeTieredExpr {
			continue
		}
		if strings.TrimSpace(expressions[model]) == "" {
			return fmt.Errorf("tiered billing mode for model %q requires a billing expression", model)
		}
	}
	return nil
}

func smokeTestExpr(exprStr string) error {
	vectors := []billingexpr.TokenParams{
		{P: 0, C: 0, Len: 0},
		{P: 1000, C: 1000, Len: 1000},
		{P: 100000, C: 100000, Len: 100000},
		{P: 1000000, C: 1000000, Len: 1000000},
	}
	requests := []billingexpr.RequestInput{
		{},
		{
			Headers: map[string]string{
				"anthropic-beta": "fast-mode-2026-02-01",
			},
			Body: []byte(`{"service_tier":"fast","stream_options":{"include_usage":true},"messages":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21]}`),
		},
	}

	for _, v := range vectors {
		for _, request := range requests {
			result, _, err := billingexpr.RunExprWithRequest(exprStr, v, request)
			if err != nil {
				return fmt.Errorf("vector {p=%g, c=%g}: run failed: %w", v.P, v.C, err)
			}
			if result < 0 {
				return fmt.Errorf("vector {p=%g, c=%g}: result %f < 0", v.P, v.C, result)
			}
		}
	}
	return nil
}
