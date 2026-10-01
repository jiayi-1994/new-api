package controller

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Explicit form tags whitelist filters. Query parameters never become SQL identifiers.
type videoAuditQuery struct {
	Start          int64    `form:"start"`
	End            int64    `form:"end"`
	Model          string   `form:"model"`
	Mode           string   `form:"mode"`
	Channel        int      `form:"channel"`
	Group          string   `form:"group"`
	Version        string   `form:"version"`
	Outcome        string   `form:"outcome"`
	Resolution     string   `form:"resolution"`
	Seconds        *float64 `form:"seconds"`
	ReferenceVideo *int     `form:"reference_video"`
	ReferenceImage *int     `form:"reference_image"`
	ReferenceAudio *int     `form:"reference_audio"`
	RequestID      string   `form:"request_id"`
	Page           int      `form:"page"`
	PageSize       int      `form:"page_size"`
}

func bindVideoAuditFilter(c *gin.Context) (model.VideoScheduleAuditFilter, error) {
	var query videoAuditQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		return model.VideoScheduleAuditFilter{}, errors.New("invalid scheduling audit filter")
	}
	return model.VideoScheduleAuditFilter{Start: query.Start, End: query.End, Model: query.Model, Mode: query.Mode, Channel: query.Channel, Group: query.Group, Version: query.Version, Outcome: query.Outcome, Resolution: query.Resolution, Seconds: query.Seconds, ReferenceVideo: query.ReferenceVideo, ReferenceImage: query.ReferenceImage, ReferenceAudio: query.ReferenceAudio, RequestID: query.RequestID, Page: query.Page, PageSize: query.PageSize}, nil
}

func ListVideoScheduleAudits(c *gin.Context) {
	filter, err := bindVideoAuditFilter(c)
	if err == nil {
		err = filter.Validate(time.Now())
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	runs, total, err := model.ListVideoScheduleAudits(ctx, filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	var bounds struct {
		First *int64 `json:"first"`
		Last  *int64 `json:"last"`
	}
	if err := model.DB.WithContext(ctx).Model(&model.VideoScheduleRun{}).Select("MIN(started_at) AS first, MAX(started_at) AS last").Scan(&bounds).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	setting := operation_setting.GetVideoSchedulingSetting()
	common.ApiSuccess(c, gin.H{"items": runs, "total": total, "filter": filter, "as_of": time.Now().UnixMilli(), "collection": service.VideoScheduleAuditCollectionStatus(), "audit_enabled": setting.AuditEnabled, "retention_days": setting.AuditRetentionDays, "data_range": bounds})
}

func GetVideoScheduleAudit(c *gin.Context) {
	requestID := c.Param("request_id")
	if requestID == "" || len(requestID) > 64 {
		common.ApiErrorMsg(c, "invalid scheduling audit request ID")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	audit, err := model.GetVideoScheduleAudit(ctx, requestID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "scheduling audit not found"})
		return
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	attempts, err := audit.Run.VideoScheduleAuditAttempts()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	healthAttempts, healthErr := model.ListVideoHealthAttempts(ctx, requestID)
	healthStatus := "supported"
	if healthErr != nil {
		healthStatus = "unavailable"
	} else if len(healthAttempts) == 0 {
		healthStatus = "no_health_facts"
	}
	common.ApiSuccess(c, gin.H{"run": audit.Run, "decisions": audit.Decisions, "attempts": attempts, "health_attempts": healthAttempts, "health_status": healthStatus})
}

func GetVideoScheduleAuditStats(c *gin.Context) {
	filter, err := bindVideoAuditFilter(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	stats, err := model.GetVideoScheduleAuditStats(ctx, filter)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	stats.MaturityWaitMS = int64(constant.TaskTimeoutMinutes) * 60000
	health, healthErr := model.GetVideoReliabilityStats(ctx, filter)
	if healthErr != nil {
		health = model.VideoReliabilityStats{Reason: "health_state_unavailable"}
	}
	health.CollectionWriteFailures = service.VideoReliabilityCollectionFailures()
	stats.Reliability = &health
	common.ApiSuccess(c, stats)
}

func ExportVideoScheduleAudits(c *gin.Context) {
	filter, err := bindVideoAuditFilter(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	data, err := model.ExportVideoScheduleAudits(ctx, filter, c.Query("continuation"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="video-scheduling-audit.ndjson"`)
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/x-ndjson", data)
}
