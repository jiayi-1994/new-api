package billing_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPricingDocumentsConcurrentPublishReturnsOneAtomicSnapshot(t *testing.T) {
	originalModes, originalExpressions := PricingDocumentsJSON()
	t.Cleanup(func() { require.NoError(t, UpdatePricingDocuments(originalModes, originalExpressions)) })

	oldModes := `{"atomic-model":"ratio"}`
	oldExpressions := `{"atomic-model":"tier(\"old\", p * 1)"}`
	newModes := `{"atomic-model":"tiered_expr"}`
	newExpressions := `{"atomic-model":"tier(\"new\", p * 2)"}`
	require.NoError(t, UpdatePricingDocuments(oldModes, oldExpressions))

	start := make(chan struct{})
	published := make(chan error, 1)
	type snapshot struct {
		mode    string
		expr    string
		hasExpr bool
	}
	read := make(chan snapshot, 1)
	go func() {
		<-start
		published <- UpdatePricingDocuments(newModes, newExpressions)
	}()
	go func() {
		<-start
		mode, expr, hasExpr := GetBillingModeAndExpr("atomic-model")
		read <- snapshot{mode: mode, expr: expr, hasExpr: hasExpr}
	}()
	close(start)

	require.NoError(t, <-published)
	observed := <-read
	assert.True(t, observed.hasExpr)
	assert.True(t,
		(observed.mode == BillingModeRatio && observed.expr == `tier("old", p * 1)`) ||
			(observed.mode == BillingModeTieredExpr && observed.expr == `tier("new", p * 2)`),
		"reader observed a mixed billing mode/expression snapshot: %#v", observed,
	)
}

func TestUpdatePricingDocumentsRejectsInvalidPairWithoutPublishingEitherMap(t *testing.T) {
	originalModes, originalExpressions := PricingDocumentsJSON()
	t.Cleanup(func() { require.NoError(t, UpdatePricingDocuments(originalModes, originalExpressions)) })
	require.NoError(t, UpdatePricingDocuments(
		`{"atomic-model":"ratio"}`,
		`{"atomic-model":"tier(\"old\", p * 1)"}`,
	))

	err := UpdatePricingDocuments(`{"atomic-model":"tiered_expr"}`, `{}`)
	require.Error(t, err)
	mode, expr, hasExpr := GetBillingModeAndExpr("atomic-model")
	assert.Equal(t, BillingModeRatio, mode)
	assert.Equal(t, `tier("old", p * 1)`, expr)
	assert.True(t, hasExpr)
}

func TestConfigLoadPublishesBillingDocumentsThroughAtomicSnapshot(t *testing.T) {
	originalModes, originalExpressions := PricingDocumentsJSON()
	t.Cleanup(func() { require.NoError(t, UpdatePricingDocuments(originalModes, originalExpressions)) })

	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode": `{"config-model":"tiered_expr"}`,
		"billing_setting.billing_expr": `{"config-model":"tier(\"config\", p * 3)"}`,
	}))
	mode, expr, hasExpr := GetBillingModeAndExpr("config-model")
	assert.Equal(t, BillingModeTieredExpr, mode)
	assert.Equal(t, `tier("config", p * 3)`, expr)
	assert.True(t, hasExpr)
}
