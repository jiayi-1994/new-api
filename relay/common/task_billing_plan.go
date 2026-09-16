package common

import (
	"fmt"
	"math"

	rootcommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

type TaskBillingKind uint8

const (
	TaskBillingKindLegacy TaskBillingKind = iota
	TaskBillingKindVideoResolution
)

type TaskBillingPlan struct {
	kind             TaskBillingKind
	originModelName  string
	requestID        string
	resolutionPrices map[string]float64
	billingUnit      string
}

func NewLegacyTaskBillingPlan(model, requestID string) *TaskBillingPlan {
	return &TaskBillingPlan{
		kind:            TaskBillingKindLegacy,
		originModelName: model,
		requestID:       requestID,
	}
}

func NewVideoResolutionTaskBillingPlan(model, requestID string, prices map[string]float64, billingUnit string) (*TaskBillingPlan, error) {
	if billingUnit != ratio_setting.TaskBillingModePerSecond && billingUnit != ratio_setting.TaskBillingModePerCall {
		return nil, fmt.Errorf("invalid video resolution billing unit %q", billingUnit)
	}
	if model == "" || requestID == "" || len(prices) == 0 {
		return nil, fmt.Errorf("video resolution billing requires model, request identity, and prices")
	}

	clone := make(map[string]float64, len(prices))
	for key, price := range prices {
		normalized, err := rootcommon.NormalizeVideoResolutionKey(key)
		if err != nil || price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			return nil, fmt.Errorf("invalid video resolution price %q", key)
		}
		clone[normalized] = price
	}

	return &TaskBillingPlan{
		kind:             TaskBillingKindVideoResolution,
		originModelName:  model,
		requestID:        requestID,
		resolutionPrices: clone,
		billingUnit:      billingUnit,
	}, nil
}

func (plan *TaskBillingPlan) Kind() TaskBillingKind {
	return plan.kind
}

func (plan *TaskBillingPlan) BillingUnit() string {
	return plan.billingUnit
}

func (plan *TaskBillingPlan) OriginModelName() string {
	return plan.originModelName
}

func (plan *TaskBillingPlan) RequestID() string {
	return plan.requestID
}

func (plan *TaskBillingPlan) ResolutionPrice(resolution string) (float64, bool) {
	if plan == nil || plan.kind != TaskBillingKindVideoResolution {
		return 0, false
	}
	normalized, err := rootcommon.NormalizeVideoResolutionKey(resolution)
	if err != nil {
		return 0, false
	}
	price, ok := plan.resolutionPrices[normalized]
	return price, ok
}
