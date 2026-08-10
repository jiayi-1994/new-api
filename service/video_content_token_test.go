package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoContentTokenRoundTripAndSevenDayExpiry(t *testing.T) {
	useTestSessionSecret(t)

	token, expiresAt, err := IssueVideoContentToken("task-public-1", 42, 99)
	require.NoError(t, err)

	grant, err := ParseVideoContentToken(token, "task-public-1")
	require.NoError(t, err)
	assert.Equal(t, 42, grant.OwnerUserID)
	assert.Equal(t, int64(99), grant.TaskRecordID)
	assert.Equal(t, expiresAt, grant.ExpiresAt)

	claims := &videoContentTokenClaims{}
	_, _, err = jwt.NewParser().ParseUnverified(token, claims)
	require.NoError(t, err)
	require.NotNil(t, claims.IssuedAt)
	require.NotNil(t, claims.ExpiresAt)
	assert.Equal(t, int64(VideoContentTokenTTL/time.Second), claims.ExpiresAt.Unix()-claims.IssuedAt.Unix())
}

func TestVideoContentTokenRejectsInvalidInput(t *testing.T) {
	useTestSessionSecret(t)

	for _, testCase := range []struct {
		name         string
		taskID       string
		ownerUserID  int
		taskRecordID int64
	}{
		{name: "blank task ID", taskID: "", ownerUserID: 42, taskRecordID: 99},
		{name: "whitespace task ID", taskID: " \t", ownerUserID: 42, taskRecordID: 99},
		{name: "zero owner ID", taskID: "task-public-1", ownerUserID: 0, taskRecordID: 99},
		{name: "negative owner ID", taskID: "task-public-1", ownerUserID: -1, taskRecordID: 99},
		{name: "zero record ID", taskID: "task-public-1", ownerUserID: 42, taskRecordID: 0},
		{name: "negative record ID", taskID: "task-public-1", ownerUserID: 42, taskRecordID: -1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, _, err := IssueVideoContentToken(testCase.taskID, testCase.ownerUserID, testCase.taskRecordID)
			assert.ErrorIs(t, err, ErrAuthTokenInvalid)
		})
	}

	for _, rawToken := range []string{"", " ", "not-a-jwt"} {
		_, err := ParseVideoContentToken(rawToken, "task-public-1")
		assert.ErrorIs(t, err, ErrAuthTokenInvalid)
	}
}

func TestVideoContentTokenRejectsInvalidClaimsAndAlgorithms(t *testing.T) {
	useTestSessionSecret(t)
	now := time.Now()

	validClaims := func() videoContentTokenClaims {
		return videoContentTokenClaims{
			TokenUse:     videoContentTokenUse,
			TaskID:       "task-public-1",
			OwnerUserID:  42,
			TaskRecordID: 99,
			Version:      videoContentTokenVersion,
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    videoContentTokenIssuer,
				Audience:  jwt.ClaimStrings{videoContentTokenAudience},
				IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
				NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)),
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
				ID:        "token-id",
			},
		}
	}

	sign := func(t *testing.T, claims videoContentTokenClaims, method jwt.SigningMethod) string {
		t.Helper()
		token, err := jwt.NewWithClaims(method, claims).SignedString(authSigningKey(videoContentTokenUse))
		require.NoError(t, err)
		return token
	}

	validToken := sign(t, validClaims(), jwt.SigningMethodHS256)
	tampered := validToken[:len(validToken)-2] + "xx"

	for _, testCase := range []struct {
		name           string
		rawToken       string
		expectedTaskID string
		expectedErr    error
	}{
		{name: "tampered", rawToken: tampered, expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "wrong task", rawToken: validToken, expectedTaskID: "task-public-2", expectedErr: ErrAuthTokenInvalid},
		{name: "expired", rawToken: func() string {
			claims := validClaims()
			claims.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Minute))
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenExpired},
		{name: "future issued at", rawToken: func() string {
			claims := validClaims()
			claims.IssuedAt = jwt.NewNumericDate(now.Add(time.Minute))
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "future not before", rawToken: func() string {
			claims := validClaims()
			claims.NotBefore = jwt.NewNumericDate(now.Add(time.Minute))
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "wrong audience", rawToken: func() string {
			claims := validClaims()
			claims.Audience = jwt.ClaimStrings{"other-audience"}
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "wrong issuer", rawToken: func() string {
			claims := validClaims()
			claims.Issuer = "other-issuer"
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "wrong use", rawToken: func() string {
			claims := validClaims()
			claims.TokenUse = "other_use"
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "wrong version", rawToken: func() string {
			claims := validClaims()
			claims.Version++
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "zero owner", rawToken: func() string {
			claims := validClaims()
			claims.OwnerUserID = 0
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "negative owner", rawToken: func() string {
			claims := validClaims()
			claims.OwnerUserID = -1
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "zero record", rawToken: func() string {
			claims := validClaims()
			claims.TaskRecordID = 0
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "negative record", rawToken: func() string {
			claims := validClaims()
			claims.TaskRecordID = -1
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "missing jti", rawToken: func() string {
			claims := validClaims()
			claims.ID = ""
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "missing issued at", rawToken: func() string {
			claims := validClaims()
			claims.IssuedAt = nil
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "missing not before", rawToken: func() string {
			claims := validClaims()
			claims.NotBefore = nil
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "missing expiration", rawToken: func() string {
			claims := validClaims()
			claims.ExpiresAt = nil
			return sign(t, claims, jwt.SigningMethodHS256)
		}(), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
		{name: "non HS256", rawToken: sign(t, validClaims(), jwt.SigningMethodHS384), expectedTaskID: "task-public-1", expectedErr: ErrAuthTokenInvalid},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseVideoContentToken(testCase.rawToken, testCase.expectedTaskID)
			assert.ErrorIs(t, err, testCase.expectedErr)
		})
	}
}

func TestVideoContentTokenRejectsSessionSecretRotationAndAccessTokens(t *testing.T) {
	useTestSessionSecret(t)
	token, _, err := IssueVideoContentToken("task-public-1", 42, 99)
	require.NoError(t, err)

	previousSecret := common.SessionSecret
	common.SessionSecret = "rotated-test-session-secret"
	t.Cleanup(func() { common.SessionSecret = previousSecret })
	_, err = ParseVideoContentToken(token, "task-public-1")
	assert.ErrorIs(t, err, ErrAuthTokenInvalid)

	common.SessionSecret = previousSecret
	accessToken, _, err := IssueAccessToken(AuthIdentity{
		UserID:          42,
		SessionID:       "session-1",
		UserAuthVersion: 1,
		SessionVersion:  1,
	})
	require.NoError(t, err)
	_, err = ParseVideoContentToken(accessToken, "task-public-1")
	assert.ErrorIs(t, err, ErrAuthTokenInvalid)
}

func TestVideoContentPublicTaskIDKeepsOnlyCurrentGatewayIDs(t *testing.T) {
	useTestSessionSecret(t)
	const gatewayID = "task_AbCdEfGhIjKlMnOpQrStUvWxYz012345"

	publicID, err := VideoContentPublicTaskID(99, gatewayID, true)
	require.NoError(t, err)
	assert.Equal(t, gatewayID, publicID)

	legacyID := "upstream/task?signature=secret"
	firstAlias, err := VideoContentPublicTaskID(99, legacyID, false)
	require.NoError(t, err)
	secondAlias, err := VideoContentPublicTaskID(99, legacyID, false)
	require.NoError(t, err)
	assert.Equal(t, firstAlias, secondAlias)
	assert.Regexp(t, `^task_[0-9a-f]{32}$`, firstAlias)
	assert.NotContains(t, firstAlias, legacyID)

	unsafeStoredID, err := VideoContentPublicTaskID(99, "task_unsafe/with-reserved?chars", true)
	require.NoError(t, err)
	assert.Regexp(t, `^task_[0-9a-f]{32}$`, unsafeStoredID)

	paddedStoredID, err := VideoContentPublicTaskID(99, " "+gatewayID+" ", true)
	require.NoError(t, err)
	assert.Regexp(t, `^task_[0-9a-f]{32}$`, paddedStoredID)

	blankStoredID, err := VideoContentPublicTaskID(99, " \t", false)
	require.NoError(t, err)
	assert.Regexp(t, `^task_[0-9a-f]{32}$`, blankStoredID)

	recordID, ok := ParseVideoContentPublicTaskID(firstAlias)
	assert.True(t, ok)
	assert.Equal(t, int64(99), recordID)
	_, ok = ParseVideoContentPublicTaskID(firstAlias[:len(firstAlias)-1] + "0")
	assert.False(t, ok)
	_, ok = ParseVideoContentPublicTaskID(gatewayID)
	assert.False(t, ok)

	for _, testCase := range []struct {
		name     string
		recordID int64
		storedID string
	}{
		{name: "zero record ID", recordID: 0, storedID: gatewayID},
		{name: "negative record ID", recordID: -1, storedID: gatewayID},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := VideoContentPublicTaskID(testCase.recordID, testCase.storedID, true)
			assert.ErrorIs(t, err, ErrAuthTokenInvalid)
		})
	}
}

func TestVideoContentTokenCarriesOnlyPublicTaskID(t *testing.T) {
	useTestSessionSecret(t)
	const upstreamTaskID = "upstream/task?X-Amz-Signature=secret"
	publicTaskID, err := VideoContentPublicTaskID(123, upstreamTaskID, false)
	require.NoError(t, err)
	token, _, err := IssueVideoContentToken(publicTaskID, 42, 123)
	require.NoError(t, err)

	claims := &videoContentTokenClaims{}
	_, _, err = jwt.NewParser().ParseUnverified(token, claims)
	require.NoError(t, err)
	assert.Equal(t, publicTaskID, claims.TaskID)
	assert.NotContains(t, claims.TaskID, upstreamTaskID)
	assert.Equal(t, int64(123), claims.TaskRecordID)
}
