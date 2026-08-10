package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
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
	videoContentPublicIDUse   = "video_content_public_id"
)

var gatewayPublicTaskIDPattern = regexp.MustCompile(`^task_[A-Za-z0-9]{32}$`)

type videoContentTokenClaims struct {
	TokenUse     string `json:"token_use"`
	TaskID       string `json:"task_id"`
	OwnerUserID  int    `json:"owner_user_id"`
	TaskRecordID int64  `json:"task_record_id"`
	Version      int    `json:"version"`
	jwt.RegisteredClaims
}

// VideoContentGrant identifies the task record and owner authorized by a video capability.
type VideoContentGrant struct {
	OwnerUserID  int
	TaskRecordID int64
	ExpiresAt    int64
}

// IssueVideoContentToken creates a capability for one completed public video task.
func IssueVideoContentToken(publicTaskID string, ownerUserID int, taskRecordID int64) (token string, expiresAt int64, err error) {
	publicTaskID = strings.TrimSpace(publicTaskID)
	if publicTaskID == "" || ownerUserID <= 0 || taskRecordID <= 0 {
		return "", 0, ErrAuthTokenInvalid
	}

	now := time.Now()
	expires := now.Add(VideoContentTokenTTL)
	claims := videoContentTokenClaims{
		TokenUse:     videoContentTokenUse,
		TaskID:       publicTaskID,
		OwnerUserID:  ownerUserID,
		TaskRecordID: taskRecordID,
		Version:      videoContentTokenVersion,
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
func ParseVideoContentToken(rawToken, expectedPublicTaskID string) (VideoContentGrant, error) {
	rawToken = strings.TrimSpace(rawToken)
	expectedPublicTaskID = strings.TrimSpace(expectedPublicTaskID)
	if rawToken == "" || expectedPublicTaskID == "" {
		return VideoContentGrant{}, ErrAuthTokenInvalid
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
			return VideoContentGrant{}, ErrAuthTokenExpired
		}
		return VideoContentGrant{}, fmt.Errorf("%w: %v", ErrAuthTokenInvalid, err)
	}
	if !parsed.Valid ||
		claims.TokenUse != videoContentTokenUse ||
		claims.TaskID != expectedPublicTaskID ||
		claims.OwnerUserID <= 0 ||
		claims.TaskRecordID <= 0 ||
		claims.Version != videoContentTokenVersion ||
		claims.ID == "" ||
		claims.IssuedAt == nil ||
		claims.NotBefore == nil ||
		claims.ExpiresAt == nil {
		return VideoContentGrant{}, ErrAuthTokenInvalid
	}

	return VideoContentGrant{
		OwnerUserID:  claims.OwnerUserID,
		TaskRecordID: claims.TaskRecordID,
		ExpiresAt:    claims.ExpiresAt.Unix(),
	}, nil
}

// VideoContentPublicTaskID returns a stable, non-sensitive task ID for video capabilities.
func VideoContentPublicTaskID(taskRecordID int64, storedTaskID string, hasSeparateUpstreamID bool) (string, error) {
	if taskRecordID <= 0 {
		return "", ErrAuthTokenInvalid
	}
	if hasSeparateUpstreamID && gatewayPublicTaskIDPattern.MatchString(storedTaskID) {
		return storedTaskID, nil
	}

	return videoContentPublicTaskAlias(taskRecordID), nil
}

// ParseVideoContentPublicTaskID verifies a derived task alias and returns its task record ID.
func ParseVideoContentPublicTaskID(publicTaskID string) (int64, bool) {
	if len(publicTaskID) != len("task_")+32 || !strings.HasPrefix(publicTaskID, "task_") {
		return 0, false
	}
	payload := publicTaskID[len("task_"):]
	recordIDValue, err := strconv.ParseUint(payload[:16], 16, 64)
	if err != nil || recordIDValue == 0 || recordIDValue > math.MaxInt64 {
		return 0, false
	}
	recordID := int64(recordIDValue)
	if !hmac.Equal([]byte(publicTaskID), []byte(videoContentPublicTaskAlias(recordID))) {
		return 0, false
	}
	return recordID, true
}

func videoContentPublicTaskAlias(taskRecordID int64) string {
	recordIDHex := fmt.Sprintf("%016x", taskRecordID)
	mac := hmac.New(sha256.New, authSigningKey(videoContentPublicIDUse))
	_, _ = mac.Write([]byte("task-record:" + recordIDHex))
	return "task_" + recordIDHex + hex.EncodeToString(mac.Sum(nil)[:8])
}
