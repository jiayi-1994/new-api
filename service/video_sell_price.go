package service

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/videosched"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// The video scheduler compares each candidate's purchase cost with what the
// request will be sold for. The sell price is estimated from the same inputs
// and rules submission billing uses (relay.RelayTaskSubmit), but read-only: it
// never touches PriceData, quota, pre-consume or settlement.

const (
	contextKeyVideoSellCache  = "video_sched_sell_cache"
	contextKeyVideoSalesFacts = "video_sales_facts"
)

// VideoSalesFacts is a unified model's sale, frozen once at the request
// entry. Every candidate quote and the submission charge read this value, so
// the profit floor and the bill always use the same price.
type VideoSalesFacts struct {
	Model        string // configured video_sales name
	Seconds      int
	Resolution   string // canonical tier
	USDPerSecond float64
}

func (f VideoSalesFacts) USD() float64 {
	return float64(f.Seconds) * f.USDPerSecond
}

func SetVideoSalesFacts(c *gin.Context, facts VideoSalesFacts) {
	c.Set(contextKeyVideoSalesFacts, facts)
}

func GetVideoSalesFacts(c *gin.Context) (VideoSalesFacts, bool) {
	facts, ok := c.Value(contextKeyVideoSalesFacts).(VideoSalesFacts)
	return facts, ok
}

// ParseVideoSalesFacts reads the requested output seconds and resolution from
// the host-parsed request body. Aliases must agree, seconds must be an exact
// integer, and the spec must be in the public price table; anything else is
// refused before a price is ever computed.
func ParseVideoSalesFacts(model string, sales billing_setting.VideoSalesModel, body any) (VideoSalesFacts, error) {
	envelope, _ := body.(map[string]any)
	fields := map[string]any{}
	switch envelope["kind"] {
	case string(jsplugin.BodyJSON):
		value, ok := envelope["value"].(map[string]any)
		if !ok {
			return VideoSalesFacts{}, errors.New("request body must be a JSON object")
		}
		fields = value
	case string(jsplugin.BodyMultipart), string(jsplugin.BodyForm):
		parts, _ := envelope["fields"].(map[string][]string)
		for name, values := range parts {
			// A repeated field stays a list, which no sales field accepts.
			fields[name] = values
			if len(values) == 1 {
				fields[name] = values[0]
			}
		}
	default:
		return VideoSalesFacts{}, errors.New("request must be a JSON or form body")
	}

	seconds := 0
	for _, name := range []string{"seconds", "duration"} {
		raw, present := fields[name]
		if !present {
			continue
		}
		value := -1
		switch v := raw.(type) {
		case float64:
			if v == math.Trunc(v) && v >= 1 && v <= relaycommon.MaxTaskDurationSeconds {
				value = int(v)
			}
		case string:
			if parsed, err := strconv.Atoi(v); err == nil && parsed >= 1 && parsed <= relaycommon.MaxTaskDurationSeconds {
				value = parsed
			}
		}
		if value < 0 {
			return VideoSalesFacts{}, fmt.Errorf("%s must be a whole number of seconds between 1 and %d", name, relaycommon.MaxTaskDurationSeconds)
		}
		if seconds != 0 && seconds != value {
			return VideoSalesFacts{}, errors.New("seconds and duration conflict")
		}
		seconds = value
	}
	if seconds == 0 {
		return VideoSalesFacts{}, errors.New("seconds is required")
	}

	tier := ""
	if raw, present := fields["resolution"]; present {
		name, _ := raw.(string)
		canonical, ok := billing_setting.CanonicalVideoTier(name)
		if !ok {
			return VideoSalesFacts{}, fmt.Errorf("resolution %q is not a supported tier", name)
		}
		tier = canonical
	}
	if raw, present := fields["size"]; present {
		size, _ := raw.(string)
		width, height, found := strings.Cut(strings.ToLower(strings.TrimSpace(size)), "x")
		w, widthErr := strconv.Atoi(width)
		h, heightErr := strconv.Atoi(height)
		if !found || widthErr != nil || heightErr != nil || w <= 0 || h <= 0 {
			return VideoSalesFacts{}, errors.New("size must be WIDTHxHEIGHT")
		}
		sizeTier := billing_setting.VideoTierForHeight(min(w, h))
		if tier != "" && tier != sizeTier {
			return VideoSalesFacts{}, errors.New("size and resolution conflict")
		}
		tier = sizeTier
	}
	if tier == "" {
		return VideoSalesFacts{}, errors.New("resolution or size is required")
	}
	price, sold := sales.Resolutions[tier]
	if !sold {
		return VideoSalesFacts{}, fmt.Errorf("%s is not sold for model %s", tier, model)
	}
	if !slices.Contains(price.Seconds, seconds) {
		return VideoSalesFacts{}, fmt.Errorf("%d seconds is not sold at %s for model %s", seconds, tier, model)
	}
	return VideoSalesFacts{Model: model, Seconds: seconds, Resolution: tier, USDPerSecond: price.USDPerSecond}, nil
}

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
	if sales, unified := GetVideoSalesFacts(c); unified {
		// A unified sale is the same for every candidate.
		usd := sales.USD() * VideoEffectiveGroupRatio(c, group)
		if _, err := common.QuotaRoundStrict(usd * common.QuotaPerUnit); err != nil {
			return videosched.SellPrice{Kind: videosched.SellUnknown}
		}
		return videoSellFromUSD(usd, false)
	}
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
