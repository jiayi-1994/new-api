package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupModelRatio(t *testing.T) {
	saved := GroupModelRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, UpdateGroupModelRatioByJSONString(saved)) })

	require.NoError(t, UpdateGroupModelRatioByJSONString(`{"sd73/80":{"seedance-2.0":0.73,"seedance-2.5":0.8}}`))

	ratio, ok := GetGroupModelRatio("sd73/80", "seedance-2.0")
	assert.True(t, ok)
	assert.Equal(t, 0.73, ratio)
	ratio, ok = GetGroupModelRatio("sd73/80", "seedance-2.5")
	assert.True(t, ok)
	assert.Equal(t, 0.8, ratio)

	_, ok = GetGroupModelRatio("sd73/80", "gpt-4o")
	assert.False(t, ok, "a model without an entry keeps the group ratio")
	_, ok = GetGroupModelRatio("default", "seedance-2.0")
	assert.False(t, ok, "another group is not affected")
}

func TestCheckGroupModelRatio(t *testing.T) {
	assert.NoError(t, CheckGroupModelRatio(`{"vip":{"m":0}}`))
	assert.Error(t, CheckGroupModelRatio(`{"vip":{"m":-1}}`))
	assert.Error(t, CheckGroupModelRatio(`{"vip":0.5}`), "a ratio must be nested under a model")
}
