package middleware

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const RouteTagKey = "route_tag"

func sanitizeLoggedRequestPath(path string) string {
	queryStart := strings.IndexByte(path, '?')
	if queryStart < 0 || queryStart == len(path)-1 {
		return path
	}

	query := path[queryStart+1:]
	var sanitized strings.Builder
	for {
		separator := strings.IndexAny(query, "&;")
		queryPart := query
		if separator >= 0 {
			queryPart = query[:separator]
		}

		key, _, _ := strings.Cut(queryPart, "=")
		decodedKey, err := url.QueryUnescape(key)
		if err == nil && decodedKey == "video_token" {
			queryPart = key + "=REDACTED"
		}
		sanitized.WriteString(queryPart)
		if separator < 0 {
			break
		}
		sanitized.WriteByte(query[separator])
		query = query[separator+1:]
	}
	return path[:queryStart+1] + sanitized.String()
}

func RouteTag(tag string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(RouteTagKey, tag)
		c.Next()
	}
}

func SetUpLogger(server *gin.Engine) {
	server.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		var requestID string
		if param.Keys != nil {
			requestID, _ = param.Keys[common.RequestIdKey].(string)
		}
		tag, _ := param.Keys[RouteTagKey].(string)
		if tag == "" {
			tag = "web"
		}
		return fmt.Sprintf("[GIN] %s | %s | %s | %3d | %13v | %15s | %7s %s\n",
			param.TimeStamp.Format("2006/01/02 - 15:04:05"),
			tag,
			requestID,
			param.StatusCode,
			param.Latency,
			param.ClientIP,
			param.Method,
			sanitizeLoggedRequestPath(param.Path),
		)
	}))
}
