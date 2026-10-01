package service

import (
	"errors"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/videosched"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// The video scheduler compares each candidate's purchase cost with what the
// request will be sold for. The sell price is estimated from the same inputs
// and rules submission billing uses (relay.RelayTaskSubmit), but read-only: it
// never touches PriceData, quota, pre-consume or settlement.

const contextKeyVideoSellCache = "video_sched_sell_cache"

// EstimateVideoSell estimates the USD sale of one candidate: plugin executes
// the request for clientModel, mapped by the channel to mappedModel (pass
// clientModel when there is no mapping), with body and action being that
// plugin's own decoded request. group is the group the channel is selected
// in (auto already expanded).
//
// A request that submission would reject for this plugin (missing or fixed
// expression, incompatible shared-model expression, failing or invalid usage
// hook, no per-call price) is unknown. A known or free pre-group price is
// cached per request by (plugin, mapped model); the effective group ratio is applied on
// every call because auto groups change it between attempts.
func EstimateVideoSell(c *gin.Context, group string, plugin *jsplugin.LoadedPlugin, clientModel, mappedModel string, body any, action string) videosched.SellPrice {
	cache, _ := c.Value(contextKeyVideoSellCache).(map[string]videosched.SellPrice)
	if cache == nil {
		cache = make(map[string]videosched.SellPrice)
		c.Set(contextKeyVideoSellCache, cache)
	}
	key := plugin.Meta.Key + "::" + mappedModel
	sell, cached := cache[key]
	if !cached {
		sell = videoSellBeforeGroup(c, plugin, clientModel, mappedModel, body, action)
		// Unknown may come from a hook timeout, so a later attempt asks again.
		if sell.Kind != videosched.SellUnknown {
			cache[key] = sell
		}
	}
	if sell.Kind != videosched.SellKnown {
		return sell
	}
	usd := sell.USD * VideoEffectiveGroupRatio(c, group)
	// A sale past the single-request quota ceiling saturates, and submission
	// pre-consume rejects a saturated quota, so it can never be sold.
	if _, err := common.QuotaRoundStrict(usd * common.QuotaPerUnit); err != nil {
		return videosched.SellPrice{Kind: videosched.SellUnknown, Estimated: sell.Estimated}
	}
	return videoSellFromUSD(usd, sell.Estimated)
}

// videoSellBeforeGroup is the sell price before the group ratio: the task
// usage expression when the model is expression-priced, otherwise the per-call
// price times the plugin's billing ratios.
func videoSellBeforeGroup(c *gin.Context, plugin *jsplugin.LoadedPlugin, clientModel, mappedModel string, body any, action string) videosched.SellPrice {
	unknown := videosched.SellPrice{Kind: videosched.SellUnknown}
	expr, exists := billing_setting.ResolveTaskBillingExpr(plugin.Meta.Key, clientModel, mappedModel)
	if exists || billing_setting.GetBillingMode(clientModel) == billing_setting.BillingModeTieredExpr {
		if !exists || billingexpr.UsesFixedPricing(expr) {
			return unknown
		}
		pinned, _ := c.Value(jsplugin.ContextKeyPinnedPlugin).(jsplugin.PinnedPlugin)
		if pinned.Generation.SharedModel(clientModel) || pinned.Generation.SharedModel(mappedModel) {
			schema, _ := plugin.Meta.UsageForModels(mappedModel, clientModel)
			if !billing_setting.TaskExprCompatible(expr, schema) {
				return unknown
			}
		}
		facts, _, err := videoUsageFacts(c, plugin, clientModel, mappedModel, body, action, "facts")
		if err != nil {
			return unknown
		}
		usd, _, err := billingexpr.RunExprWithRequest(expr, billingexpr.TokenParams{}, billingexpr.RequestInput{Usage: facts})
		if err != nil {
			return unknown
		}
		return videoSellFromUSD(usd, len(facts) > 0)
	}

	price, ok := ratio_setting.GetModelPrice(clientModel, false)
	if !ok {
		price, ok = ratio_setting.GetDefaultModelPriceMap()[clientModel]
	}
	if !ok {
		// Ratio-priced models have no per-call sale to compare against.
		return unknown
	}
	_, ratios, err := videoUsageFacts(c, plugin, clientModel, mappedModel, body, action, "billing_ratios")
	if err != nil {
		return unknown
	}
	if common.StringsContains(constant.TaskPricePatches, clientModel) {
		return videoSellFromUSD(price, false)
	}
	// A scratch PriceData applies the ratios with submission's exact rules.
	var priceData hosttypes.PriceData
	for name, ratio := range ratios {
		priceData.AddOtherRatio(name, ratio)
	}
	return videoSellFromUSD(priceData.ApplyOtherRatiosToFloat(price), len(ratios) > 0)
}

// videoSellFromUSD classifies an estimated sale: zero is free, a positive
// finite amount is known, anything else cannot be estimated.
func videoSellFromUSD(usd float64, estimated bool) videosched.SellPrice {
	switch {
	case usd == 0:
		return videosched.SellPrice{Kind: videosched.SellFree, Estimated: estimated}
	case usd > 0 && !math.IsInf(usd, 1):
		return videosched.SellPrice{Kind: videosched.SellKnown, USD: usd, Estimated: estimated}
	}
	return videosched.SellPrice{Kind: videosched.SellUnknown, Estimated: estimated}
}

// videoUsageFacts calls the plugin's extractUsage for purpose and validates
// the answer as submission does, returning the normalized facts and their
// positive numeric ratios. A plugin without the hook reports no facts.
func videoUsageFacts(c *gin.Context, plugin *jsplugin.LoadedPlugin, clientModel, mappedModel string, body any, action, purpose string) (map[string]any, map[string]float64, error) {
	ctx := c.Request.Context()
	if callable, err := plugin.Engine.HasCallableHook(ctx, "extractUsage"); err != nil || !callable {
		return nil, nil, nil
	}
	value, err := plugin.Engine.Call(ctx, "extractUsage", videoUsageContext(c, clientModel, mappedModel, body, action, purpose))
	if err != nil {
		return nil, nil, err
	}
	if value == nil {
		return nil, nil, nil
	}
	facts, ok := value.(map[string]any)
	if !ok {
		return nil, nil, errors.New("plugin usage hook must return an object")
	}
	ratios, err := plugin.Meta.ValidateUsageFacts(facts, mappedModel, clientModel)
	if err != nil {
		return nil, nil, err
	}
	return facts, ratios, nil
}

// videoUsageContext is the credential-free usage hook argument for one
// candidate plugin. It carries that plugin's own decoded body, never
// task_request, which belongs to the first accepted plugin.
func videoUsageContext(c *gin.Context, clientModel, mappedModel string, body any, action, purpose string) map[string]any {
	route, _ := c.Value(jsplugin.ContextKeyRouteRequest).(jsplugin.RouteRequestContext)
	if route.Path == "" && c.Request != nil {
		// The legacy /v1/tasks/:key entry prepares no route context; submission
		// fills the same fields from the HTTP request.
		route.Path, route.Method, route.Query = c.Request.URL.Path, c.Request.Method, c.Request.URL.Query()
		route.Params = make(map[string]string, len(c.Params))
		for _, param := range c.Params {
			route.Params[param.Key] = param.Value
		}
	}
	route.RequestBody = body
	files := route.Files
	if files == nil {
		files = make([]map[string]any, 0)
	}
	return jsplugin.BuildUsageContext(jsplugin.UsageContext{
		Route:         route,
		Headers:       map[string]string{"Content-Type": c.GetHeader("Content-Type"), "Accept": c.GetHeader("Accept")},
		Files:         files,
		Action:        action,
		Model:         clientModel,
		UpstreamModel: mappedModel,
		UsagePurpose:  purpose,
	})
}

// VideoEffectiveGroupRatio is the group ratio submission charges a request in
// group: the user group's special ratio for it when one exists, else the
// group's own ratio.
func VideoEffectiveGroupRatio(c *gin.Context, group string) float64 {
	ratio, special := ratio_setting.GetGroupGroupRatio(common.GetContextKeyString(c, constant.ContextKeyUserGroup), group)
	if !special {
		ratio = ratio_setting.GetGroupRatio(group)
	}
	return ratio
}
