package relay

import (
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/advancedcustom"
	"github.com/QuantumNous/new-api/relay/channel/ali"
	"github.com/QuantumNous/new-api/relay/channel/aws"
	"github.com/QuantumNous/new-api/relay/channel/baidu"
	"github.com/QuantumNous/new-api/relay/channel/baidu_v2"
	"github.com/QuantumNous/new-api/relay/channel/claude"
	"github.com/QuantumNous/new-api/relay/channel/cloudflare"
	"github.com/QuantumNous/new-api/relay/channel/codex"
	"github.com/QuantumNous/new-api/relay/channel/cohere"
	"github.com/QuantumNous/new-api/relay/channel/coze"
	"github.com/QuantumNous/new-api/relay/channel/deepseek"
	"github.com/QuantumNous/new-api/relay/channel/dify"
	"github.com/QuantumNous/new-api/relay/channel/gemini"
	"github.com/QuantumNous/new-api/relay/channel/jimeng"
	"github.com/QuantumNous/new-api/relay/channel/jina"
	"github.com/QuantumNous/new-api/relay/channel/minimax"
	"github.com/QuantumNous/new-api/relay/channel/mistral"
	"github.com/QuantumNous/new-api/relay/channel/mokaai"
	"github.com/QuantumNous/new-api/relay/channel/moonshot"
	"github.com/QuantumNous/new-api/relay/channel/newapi"
	"github.com/QuantumNous/new-api/relay/channel/ollama"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	"github.com/QuantumNous/new-api/relay/channel/palm"
	"github.com/QuantumNous/new-api/relay/channel/perplexity"
	"github.com/QuantumNous/new-api/relay/channel/replicate"
	"github.com/QuantumNous/new-api/relay/channel/siliconflow"
	"github.com/QuantumNous/new-api/relay/channel/sub2api"
	"github.com/QuantumNous/new-api/relay/channel/submodel"
	taskali "github.com/QuantumNous/new-api/relay/channel/task/ali"
	taskdoubao "github.com/QuantumNous/new-api/relay/channel/task/doubao"
	taskGemini "github.com/QuantumNous/new-api/relay/channel/task/gemini"
	"github.com/QuantumNous/new-api/relay/channel/task/hailuo"
	taskjimeng "github.com/QuantumNous/new-api/relay/channel/task/jimeng"
	"github.com/QuantumNous/new-api/relay/channel/task/kling"
	tasksora "github.com/QuantumNous/new-api/relay/channel/task/sora"
	"github.com/QuantumNous/new-api/relay/channel/task/suno"
	taskvertex "github.com/QuantumNous/new-api/relay/channel/task/vertex"
	taskVidu "github.com/QuantumNous/new-api/relay/channel/task/vidu"
	"github.com/QuantumNous/new-api/relay/channel/tencent"
	"github.com/QuantumNous/new-api/relay/channel/vertex"
	"github.com/QuantumNous/new-api/relay/channel/volcengine"
	"github.com/QuantumNous/new-api/relay/channel/xai"
	"github.com/QuantumNous/new-api/relay/channel/xunfei"
	"github.com/QuantumNous/new-api/relay/channel/zhipu"
	"github.com/QuantumNous/new-api/relay/channel/zhipu_4v"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

const taskBillingPlanContextKey = "task_billing_plan"

// PrepareTaskBillingPlan freezes task billing before any channel is selected.
// Repeated calls reuse the request-scoped plan so retries and task relay share
// the same pricing kind, source model, request identity, and price table.
func PrepareTaskBillingPlan(c *gin.Context, modelName, requestID string) (*relaycommon.TaskBillingPlan, error) {
	if value, ok := c.Get(taskBillingPlanContextKey); ok {
		if plan, ok := value.(*relaycommon.TaskBillingPlan); ok && plan != nil {
			return plan, nil
		}
	}
	if requestID == "" {
		requestID = c.GetString(common.RequestIdKey)
	}
	if requestID == "" {
		requestID = common.NewRequestId()
		c.Set(common.RequestIdKey, requestID)
	}

	isSuno := constant.TaskPlatform(c.GetString("platform")) == constant.TaskPlatformSuno || constant.IsSunoModel(modelName)
	var prices map[string]float64
	if !isSuno {
		prices, _ = ratio_setting.GetVideoResolutionPrices(modelName)
	}
	plan, err := makeTaskBillingPlan(modelName, requestID, isSuno, prices)
	if err != nil {
		return nil, err
	}
	c.Set(taskBillingPlanContextKey, plan)
	return plan, nil
}

// refreezeTaskBillingPlanForModel 替换一个在模型名可知之前（空名）冻结的计划：
// distributor 对缺失 model 字段的提交会提前冻结 legacy("") 计划，激活判定必须
// 以最终推导的 origin 模型名重做，并同步替换请求级缓存。只能在该请求创建任何
// 计费会话之前调用；重试携带的已是重冻后的计划，不会再次进入。
func refreezeTaskBillingPlanForModel(c *gin.Context, modelName, requestID string) (*relaycommon.TaskBillingPlan, error) {
	isSuno := constant.TaskPlatform(c.GetString("platform")) == constant.TaskPlatformSuno || constant.IsSunoModel(modelName)
	var prices map[string]float64
	if !isSuno {
		prices, _ = ratio_setting.GetVideoResolutionPrices(modelName)
	}
	plan, err := makeTaskBillingPlan(modelName, requestID, isSuno, prices)
	if err != nil {
		return nil, err
	}
	c.Set(taskBillingPlanContextKey, plan)
	return plan, nil
}

func makeTaskBillingPlan(modelName, requestID string, isSuno bool, prices map[string]float64) (*relaycommon.TaskBillingPlan, error) {
	if isSuno || len(prices) == 0 {
		return relaycommon.NewLegacyTaskBillingPlan(modelName, requestID), nil
	}
	plan, err := relaycommon.NewVideoResolutionTaskBillingPlan(modelName, requestID, prices)
	if err != nil {
		return nil, fmt.Errorf("prepare video resolution billing plan: %w", err)
	}
	return plan, nil
}

var taskChannelTypes = []int{
	constant.ChannelTypeAli,
	constant.ChannelTypeKling,
	constant.ChannelTypeJimeng,
	constant.ChannelTypeVertexAi,
	constant.ChannelTypeVidu,
	constant.ChannelTypeDoubaoVideo,
	constant.ChannelTypeVolcEngine,
	constant.ChannelTypeSora,
	constant.ChannelTypeOpenAI,
	constant.ChannelTypeGemini,
	constant.ChannelTypeMiniMax,
}

// CompatibleTaskChannelTypes returns task channel types that can execute the
// frozen billing plan. A nil result for legacy preserves existing selection.
func CompatibleTaskChannelTypes(kind relaycommon.TaskBillingKind) []int {
	if kind != relaycommon.TaskBillingKindVideoResolution {
		return nil
	}

	allowed := make([]int, 0, len(taskChannelTypes))
	for _, channelType := range taskChannelTypes {
		adaptor := GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(channelType)))
		if _, ok := adaptor.(channel.VideoBillingResolver); ok {
			allowed = append(allowed, channelType)
		}
	}
	return allowed
}

func TaskChannelTypeSupportsBilling(kind relaycommon.TaskBillingKind, channelType int) bool {
	if kind != relaycommon.TaskBillingKindVideoResolution {
		return true
	}
	for _, compatibleType := range CompatibleTaskChannelTypes(kind) {
		if compatibleType == channelType {
			return true
		}
	}
	return false
}

func GetAdaptor(apiType int) channel.Adaptor {
	switch apiType {
	case constant.APITypeAli:
		return &ali.Adaptor{}
	case constant.APITypeAnthropic:
		return &claude.Adaptor{}
	case constant.APITypeBaidu:
		return &baidu.Adaptor{}
	case constant.APITypeGemini:
		return &gemini.Adaptor{}
	case constant.APITypeOpenAI:
		return &openai.Adaptor{}
	case constant.APITypePaLM:
		return &palm.Adaptor{}
	case constant.APITypeTencent:
		return &tencent.DispatchAdaptor{}
	case constant.APITypeXunfei:
		return &xunfei.Adaptor{}
	case constant.APITypeZhipu:
		return &zhipu.Adaptor{}
	case constant.APITypeZhipuV4:
		return &zhipu_4v.Adaptor{}
	case constant.APITypeOllama:
		return &ollama.Adaptor{}
	case constant.APITypePerplexity:
		return &perplexity.Adaptor{}
	case constant.APITypeAws:
		return &aws.Adaptor{}
	case constant.APITypeCohere:
		return &cohere.Adaptor{}
	case constant.APITypeDify:
		return &dify.Adaptor{}
	case constant.APITypeJina:
		return &jina.Adaptor{}
	case constant.APITypeCloudflare:
		return &cloudflare.Adaptor{}
	case constant.APITypeSiliconFlow:
		return &siliconflow.Adaptor{}
	case constant.APITypeVertexAi:
		return &vertex.Adaptor{}
	case constant.APITypeMistral:
		return &mistral.Adaptor{}
	case constant.APITypeDeepSeek:
		return &deepseek.Adaptor{}
	case constant.APITypeMokaAI:
		return &mokaai.Adaptor{}
	case constant.APITypeVolcEngine:
		return &volcengine.Adaptor{}
	case constant.APITypeBaiduV2:
		return &baidu_v2.Adaptor{}
	case constant.APITypeOpenRouter:
		return &openai.Adaptor{}
	case constant.APITypeXinference:
		return &openai.Adaptor{}
	case constant.APITypeXai:
		return &xai.Adaptor{}
	case constant.APITypeCoze:
		return &coze.Adaptor{}
	case constant.APITypeJimeng:
		return &jimeng.Adaptor{}
	case constant.APITypeMoonshot:
		return &moonshot.Adaptor{} // Moonshot uses Claude API
	case constant.APITypeSubmodel:
		return &submodel.Adaptor{}
	case constant.APITypeMiniMax:
		return &minimax.Adaptor{}
	case constant.APITypeReplicate:
		return &replicate.Adaptor{}
	case constant.APITypeCodex:
		return &codex.Adaptor{}
	case constant.APITypeAdvancedCustom:
		return &advancedcustom.Adaptor{}
	case constant.APITypeSub2API:
		return &sub2api.Adaptor{}
	case constant.APITypeNewAPI:
		return &newapi.Adaptor{}
	}
	return nil
}

func GetTaskPlatform(c *gin.Context) constant.TaskPlatform {
	channelType := c.GetInt("channel_type")
	if channelType > 0 {
		return constant.TaskPlatform(strconv.Itoa(channelType))
	}
	return constant.TaskPlatform(c.GetString("platform"))
}

func GetTaskAdaptor(platform constant.TaskPlatform) channel.TaskAdaptor {
	switch platform {
	//case constant.APITypeAIProxyLibrary:
	//	return &aiproxy.Adaptor{}
	case constant.TaskPlatformSuno:
		return &suno.TaskAdaptor{}
	}
	if channelType, err := strconv.ParseInt(string(platform), 10, 64); err == nil {
		switch channelType {
		case constant.ChannelTypeAli:
			return &taskali.TaskAdaptor{}
		case constant.ChannelTypeKling:
			return &kling.TaskAdaptor{}
		case constant.ChannelTypeJimeng:
			return &taskjimeng.TaskAdaptor{}
		case constant.ChannelTypeVertexAi:
			return &taskvertex.TaskAdaptor{}
		case constant.ChannelTypeVidu:
			return &taskVidu.TaskAdaptor{}
		case constant.ChannelTypeDoubaoVideo, constant.ChannelTypeVolcEngine:
			return &taskdoubao.TaskAdaptor{}
		case constant.ChannelTypeSora, constant.ChannelTypeOpenAI:
			return &tasksora.TaskAdaptor{}
		case constant.ChannelTypeGemini:
			return &taskGemini.TaskAdaptor{}
		case constant.ChannelTypeMiniMax:
			return &hailuo.TaskAdaptor{}
		}
	}
	return nil
}
