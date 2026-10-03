package service

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/videosched"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// RequestVideoHealthRecovery makes a blocked model eligible for bounded
// verification. Only subsequent upstream results can restore normal routing.
func RequestVideoHealthRecovery(ctx context.Context, channelID int, modelName string, expectedVersion int64) (*model.VideoHealthState, error) {
	setting := operation_setting.GetVideoSchedulingSetting()
	if setting.Mode != "on" || setting.SelectionPolicy != videosched.PolicyStabilityCostV2 {
		return nil, errors.New("manual recovery requires active stability-cost video scheduling")
	}
	if setting.ProbeMaxInFlight < 1 {
		return nil, errors.New("video recovery verification is disabled")
	}
	channel, err := model.GetChannelById(channelID, true)
	if err != nil {
		return nil, err
	}
	config, configured := VideoSchedulingConfigOf(channel)
	if !configured {
		return nil, errors.New("video recovery requires a scheduled channel")
	}
	if _, priced := videoModelCost(config.Models, modelName); !priced {
		return nil, errors.New("video recovery model is not configured on this channel")
	}
	state, err := model.RequestVideoHealthRecovery(ctx, channelID, modelName, expectedVersion)
	if err != nil {
		return nil, err
	}
	// The database transition is authoritative. A cache outage must not turn a
	// committed recovery into a failed operation that invites a duplicate retry.
	// This existing publisher invalidates failed writes for later reconciliation.
	publishPersistedVideoReliability(ctx, channelID, modelName)
	return state, nil
}
