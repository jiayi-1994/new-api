package common

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
)

// MapModelName follows a channel's model_mapping JSON from origin to the end
// of its redirect chain, trying a hop's reasoning base name when the hop itself
// is not mapped. mapped is false when nothing redirects origin to another
// model; a chain that ends in a self-map stops at that hop.
func MapModelName(modelMapping, origin string) (upstream string, mapped bool, err error) {
	if modelMapping == "" || modelMapping == "{}" {
		return origin, false, nil
	}
	modelMap := make(map[string]string)
	if err := common.Unmarshal([]byte(modelMapping), &modelMap); err != nil {
		return origin, false, errors.New("unmarshal_model_mapping_failed")
	}
	current := origin
	visited := map[string]bool{current: true}
	for {
		next, exists := modelMap[current]
		if base := hostreasoning.BaseModelName(current); (!exists || next == "") && base != current {
			next, exists = modelMap[base]
		}
		if !exists || next == "" {
			return current, mapped, nil
		}
		if visited[next] {
			if next != current {
				return origin, false, errors.New("model_mapping_contains_cycle")
			}
			return current, mapped, nil
		}
		visited[next] = true
		current = next
		mapped = true
	}
}
