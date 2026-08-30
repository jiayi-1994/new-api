package service

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
)

func TestRetryParamCarriesAllowedChannelTypesAcrossRetries(t *testing.T) {
	param := RetryParam{AllowedChannelTypes: []int{constant.ChannelTypeSora}}
	param.IncreaseRetry()

	assert.Equal(t, []int{constant.ChannelTypeSora}, param.AllowedChannelTypes)
	assert.Equal(t, 1, param.GetRetry())
}
