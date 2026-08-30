package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsesResolutionReservationLedgerReadsFrozenKind(t *testing.T) {
	resolution, err := relaycommon.NewVideoResolutionTaskBillingPlan(
		"video", "req-resolution", map[string]float64{"720p": 0.1},
	)
	require.NoError(t, err)
	legacy := relaycommon.NewLegacyTaskBillingPlan("video", "req-legacy")

	assert.True(t, usesResolutionReservationLedger(&relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{BillingPlan: resolution},
	}))
	assert.False(t, usesResolutionReservationLedger(&relaycommon.RelayInfo{
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			BillingPlan:          legacy,
			ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{},
		},
	}))
}

func TestNewBillingSessionUsesFrozenResolutionKindAndRequestIdentity(t *testing.T) {
	truncate(t)
	const userID = 601
	seedUser(t, userID, 10_000)
	plan, err := relaycommon.NewVideoResolutionTaskBillingPlan(
		"video", "req-frozen", map[string]float64{"720p": 0.1},
	)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{
		RequestId:       "req-live-mutated",
		UserId:          userID,
		OriginModelName: "video",
		ForcePreConsume: true,
		IsPlayground:    true,
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{BillingPlan: plan},
	}
	info.UserSetting.BillingPreference = "wallet_only"
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())

	session, apiErr := NewBillingSession(c, info, 100)

	require.Nil(t, apiErr)
	require.NotNil(t, session)
	reservation, ok := session.funding.(*ResolutionReservationFunding)
	require.True(t, ok)
	assert.Equal(t, "req-frozen", reservation.requestId)
	assert.Equal(t, 100, session.GetPreConsumedQuota())
	require.NoError(t, session.Reserve(150))
	assert.Same(t, reservation, session.funding)
	assert.Equal(t, 150, session.GetPreConsumedQuota())
	var record model.ResolutionBillingReservation
	require.NoError(t, model.DB.Where("request_id = ?", "req-frozen").First(&record).Error)
	assert.Equal(t, 150, record.Quota)
}

func TestNewBillingSessionLegacyKindIgnoresResolvedVideoBilling(t *testing.T) {
	truncate(t)
	const userID = 602
	seedUser(t, userID, 10_000)
	info := &relaycommon.RelayInfo{
		RequestId:       "req-legacy-live",
		UserId:          userID,
		OriginModelName: "video",
		ForcePreConsume: true,
		IsPlayground:    true,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			BillingPlan:          relaycommon.NewLegacyTaskBillingPlan("video", "req-legacy-frozen"),
			ResolvedVideoBilling: &relaycommon.ResolvedVideoBilling{},
		},
	}
	info.UserSetting.BillingPreference = "wallet_only"
	c := gin.CreateTestContextOnly(httptest.NewRecorder(), gin.New())

	session, apiErr := NewBillingSession(c, info, 100)

	require.Nil(t, apiErr)
	require.NotNil(t, session)
	_, usesReservation := session.funding.(*ResolutionReservationFunding)
	assert.False(t, usesReservation)
	_, usesWallet := session.funding.(*WalletFunding)
	assert.True(t, usesWallet)
}
