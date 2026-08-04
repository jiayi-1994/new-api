package setting

import "github.com/QuantumNous/new-api/common"

// 外部商店入口（“充值购买”菜单）配置，仅由环境变量控制：
//
//	SHOP_LINK_ENABLED  是否在侧边栏“钱包”下方显示该入口，默认关闭
//	SHOP_LINK_URL      入口指向的外部地址，点击后在新标签页打开
var (
	ShopLinkEnabled bool
	ShopLinkUrl     string
)

// InitShopLinkSetting 读取商店入口相关的环境变量。
// 必须在 godotenv 加载 .env 之后调用，否则 .env 中的取值不会生效。
func InitShopLinkSetting() {
	ShopLinkEnabled = common.GetEnvOrDefaultBool("SHOP_LINK_ENABLED", false)
	ShopLinkUrl = common.GetEnvOrDefaultString("SHOP_LINK_URL", "https://pay.ldxp.cn/shop/FWJQYF6M/klxgc9")
}
