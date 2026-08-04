package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInitShopLinkSetting(t *testing.T) {
	originalEnabled := ShopLinkEnabled
	originalURL := ShopLinkUrl
	t.Cleanup(func() {
		ShopLinkEnabled = originalEnabled
		ShopLinkUrl = originalURL
	})

	t.Run("uses the configured default when the URL is empty", func(t *testing.T) {
		t.Setenv("SHOP_LINK_ENABLED", "")
		t.Setenv("SHOP_LINK_URL", "")

		InitShopLinkSetting()

		assert.False(t, ShopLinkEnabled)
		assert.Equal(t, "https://pay.ldxp.cn/shop/FWJQYF6M/klxgc9", ShopLinkUrl)
	})

	t.Run("preserves an explicit environment override", func(t *testing.T) {
		t.Setenv("SHOP_LINK_ENABLED", "true")
		t.Setenv("SHOP_LINK_URL", "https://example.test/custom-shop")

		InitShopLinkSetting()

		assert.True(t, ShopLinkEnabled)
		assert.Equal(t, "https://example.test/custom-shop", ShopLinkUrl)
	})
}
