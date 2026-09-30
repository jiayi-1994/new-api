package helper

import (
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
)

func ModelMappedHelper(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) error {
	if info.ChannelMeta == nil {
		info.ChannelMeta = &relaycommon.ChannelMeta{}
	}

	// map model name
	upstream, mapped, err := relaycommon.MapModelName(c.GetString("model_mapping"), info.OriginModelName)
	if err != nil {
		return err
	}
	if mapped {
		info.IsModelMapped = true
		info.UpstreamModelName = upstream
	}

	if request != nil {
		request.SetModelName(info.UpstreamModelName)
	}
	return nil
}
