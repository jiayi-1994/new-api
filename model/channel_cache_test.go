package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRandomSatisfiedChannelForTypesFiltersBeforePrioritySelection(t *testing.T) {
	const (
		group = "resolution-filter-group"
		name  = "resolution-filter-model"
		kling = 991201
		sora  = 991202
	)

	for _, memoryCacheEnabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("memory_cache_%t", memoryCacheEnabled), func(t *testing.T) {
			previousMemoryCacheEnabled := common.MemoryCacheEnabled
			common.MemoryCacheEnabled = memoryCacheEnabled
			t.Cleanup(func() {
				common.MemoryCacheEnabled = previousMemoryCacheEnabled
				require.NoError(t, DB.Where("channel_id IN ?", []int{kling, sora}).Delete(&Ability{}).Error)
				require.NoError(t, DB.Where("id IN ?", []int{kling, sora}).Delete(&Channel{}).Error)
				InitChannelCache()
			})

			require.NoError(t, DB.Where("channel_id IN ?", []int{kling, sora}).Delete(&Ability{}).Error)
			require.NoError(t, DB.Where("id IN ?", []int{kling, sora}).Delete(&Channel{}).Error)
			highPriority, lowPriority := int64(100), int64(1)
			require.NoError(t, DB.Create(&[]Channel{
				{Id: kling, Type: constant.ChannelTypeKling, Key: "kling-key", Name: "kling", Status: common.ChannelStatusEnabled, Group: group, Models: name, Priority: &highPriority},
				{Id: sora, Type: constant.ChannelTypeSora, Key: "sora-key", Name: "sora", Status: common.ChannelStatusEnabled, Group: group, Models: name, Priority: &lowPriority},
			}).Error)
			require.NoError(t, DB.Create(&[]Ability{
				{Group: group, Model: name, ChannelId: kling, Enabled: true, Priority: &highPriority},
				{Group: group, Model: name, ChannelId: sora, Enabled: true, Priority: &lowPriority},
			}).Error)
			InitChannelCache()

			selected, err := GetRandomSatisfiedChannelForTypes(group, name, 0, "/v1/videos", []int{constant.ChannelTypeSora})
			require.NoError(t, err)
			require.NotNil(t, selected)
			assert.Equal(t, constant.ChannelTypeSora, selected.Type)

			retrySelected, err := GetRandomSatisfiedChannelForTypes(group, name, 1, "/v1/videos", []int{constant.ChannelTypeSora})
			require.NoError(t, err)
			require.NotNil(t, retrySelected)
			assert.Equal(t, constant.ChannelTypeSora, retrySelected.Type)

			legacy, err := GetRandomSatisfiedChannelForTypes(group, name, 0, "/v1/videos", nil)
			require.NoError(t, err)
			require.NotNil(t, legacy)
			assert.Equal(t, constant.ChannelTypeKling, legacy.Type)
		})
	}
}
