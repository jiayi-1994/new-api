package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	VideoContentTokenTTL      = 7 * 24 * time.Hour
	videoContentTokenUse      = "video_content"
	videoContentTokenIssuer   = "new-api-video-content"
	videoContentTokenAudience = "new-api-video-content"
	videoContentTokenVersion  = 1
)

type videoContentTokenClaims struct {
	TokenUse    string `json:"token_use"`
	TaskID      string `json:"task_id"`
	OwnerUserID int    `json:"owner_user_id"`
	Version     int    `json:"version"`
	jwt.RegisteredClaims
}

// IssueVideoContentToken creates a capability for one completed public video task.
func IssueVideoContentToken(taskID string, ownerUserID int) (token string, expiresAt int64, err error) {
	if strings.TrimSpace(taskID) == "" || ownerUserID <= 0 {
		return "", 0, ErrAuthTokenInvalid
	}

	now := time.Now()
	expires := now.Add(VideoContentTokenTTL)
	claims := videoContentTokenClaims{
		TokenUse:    videoContentTokenUse,
		TaskID:      taskID,
		OwnerUserID: ownerUserID,
		Version:     videoContentTokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    videoContentTokenIssuer,
			Audience:  jwt.ClaimStrings{videoContentTokenAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expires),
			ID:        uuid.NewString(),
		},
	}

	token, err = jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(authSigningKey(videoContentTokenUse))
	if err != nil {
		return "", 0, err
	}
	return token, expires.Unix(), nil
}

// ParseVideoContentToken validates a video capability for its route task ID.
func ParseVideoContentToken(rawToken, expectedTaskID string) (ownerUserID int, expiresAt int64, err error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" || strings.TrimSpace(expectedTaskID) == "" {
		return 0, 0, ErrAuthTokenInvalid
	}

	claims := &videoContentTokenClaims{}
	parsed, err := jwt.ParseWithClaims(rawToken, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodHS256.Alg() {
			return nil, fmt.Errorf("%w: unexpected signing method", ErrAuthTokenInvalid)
		}
		return authSigningKey(videoContentTokenUse), nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(videoContentTokenIssuer),
		jwt.WithAudience(videoContentTokenAudience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithLeeway(5*time.Second),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return 0, 0, ErrAuthTokenExpired
		}
		return 0, 0, fmt.Errorf("%w: %v", ErrAuthTokenInvalid, err)
	}
	if !parsed.Valid ||
		claims.TokenUse != videoContentTokenUse ||
		claims.TaskID != expectedTaskID ||
		claims.OwnerUserID <= 0 ||
		claims.Version != videoContentTokenVersion ||
		claims.ID == "" ||
		claims.IssuedAt == nil ||
		claims.NotBefore == nil ||
		claims.ExpiresAt == nil {
		return 0, 0, ErrAuthTokenInvalid
	}

	return claims.OwnerUserID, claims.ExpiresAt.Unix(), nil
}
