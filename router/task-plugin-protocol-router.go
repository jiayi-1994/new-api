package router

import (
	"fmt"
	"slices"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

func SetTaskPluginProtocolRouter(router *gin.Engine) {
	for _, protocol := range pluginruntime.HostProtocols() {
		for _, operation := range protocol.Operations {
			for _, method := range operation.Methods {
				handlers, err := taskPluginProtocolHandlers(protocol.Name, operation.Name)
				if err != nil {
					panic(err)
				}
				router.Handle(method, operation.Path, handlers...)
			}
		}
	}
	// Volcengine Ark video task protocol: the same OpenAI video chains, with
	// the Ark envelope outside authentication and the body conversion after it.
	router.POST(controller.ArkVideoTasksPath, slices.Concat(
		[]gin.HandlerFunc{controller.ArkVideoEnvelope()},
		openAIVideoCreateAuthHandlers(),
		[]gin.HandlerFunc{controller.ArkVideoRequest()},
		openAIVideoCreateRelayHandlers(),
	)...)
	router.GET(controller.ArkVideoTaskPath, slices.Concat(
		[]gin.HandlerFunc{controller.ArkVideoEnvelope()},
		openAIVideoRetrieveHandlers(),
	)...)
}

// openAIVideoCreateAuthHandlers authenticate a video create request before
// anything reads its body.
func openAIVideoCreateAuthHandlers() []gin.HandlerFunc {
	return []gin.HandlerFunc{middleware.RouteTag("relay"), middleware.TokenAuth(), middleware.SystemPerformanceCheck()}
}

// openAIVideoCreateRelayHandlers resolve, schedule and submit an
// authenticated OpenAI video create body.
func openAIVideoCreateRelayHandlers() []gin.HandlerFunc {
	return []gin.HandlerFunc{
		middleware.PinTaskPluginEndpoint(), middleware.TaskPluginEndpointOnly(middleware.ModelRequestRateLimit()), middleware.PrepareTaskPluginEndpoint(), middleware.Distribute(),
		func(c *gin.Context) { controller.RelayTaskPluginEndpoint(c, controller.RelayTask) },
	}
}

func openAIVideoRetrieveHandlers() []gin.HandlerFunc {
	return []gin.HandlerFunc{middleware.RouteTag("relay"), middleware.TokenAuth(), middleware.Distribute(), controller.RelayTaskFetch}
}

func taskPluginProtocolHandlers(protocol, operation string) ([]gin.HandlerFunc, error) {
	switch protocol + "." + operation {
	case "openai_responses.create":
		return []gin.HandlerFunc{
			middleware.RouteTag("relay"), middleware.SystemPerformanceCheck(), middleware.TokenAuth(),
			middleware.ModelRequestRateLimit(), middleware.PinTaskPluginEndpoint(), middleware.PrepareTaskPluginEndpoint(), middleware.Distribute(),
			func(c *gin.Context) {
				controller.RelayTaskPluginEndpoint(c, func(c *gin.Context) { controller.Relay(c, types.RelayFormatOpenAIResponses) })
			},
		}, nil
	case "openai_image.generate", "openai_image.edit":
		return []gin.HandlerFunc{
			middleware.RouteTag("relay"), middleware.SystemPerformanceCheck(), middleware.TokenAuth(),
			middleware.ModelRequestRateLimit(), middleware.PinTaskPluginEndpoint(), middleware.PrepareTaskPluginEndpoint(), middleware.Distribute(),
			func(c *gin.Context) {
				controller.RelayTaskPluginEndpoint(c, func(c *gin.Context) { controller.Relay(c, types.RelayFormatOpenAIImage) })
			},
		}, nil
	case "openai_video.create":
		return slices.Concat(openAIVideoCreateAuthHandlers(), openAIVideoCreateRelayHandlers()), nil
	case "openai_responses.retrieve":
		return []gin.HandlerFunc{middleware.RouteTag("relay"), middleware.TokenAuth(), controller.RetrieveTaskPluginResponse}, nil
	case "openai_video.retrieve":
		return openAIVideoRetrieveHandlers(), nil
	case "openai_video.content":
		return []gin.HandlerFunc{middleware.RouteTag("relay"), middleware.TokenAuth(), controller.VideoProxy}, nil
	default:
		return nil, fmt.Errorf("host protocol registry operation %s.%s has no handler", protocol, operation)
	}
}
