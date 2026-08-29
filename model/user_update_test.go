package model

import (
	"errors"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupUserUpdateTestState(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM users").Error)

	oldRedisEnabled := common.RedisEnabled
	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})
}

func TestUserUpdateDoesNotOverwriteAccountingFields(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:           1,
		Username:     "quota-race-user",
		Password:     "password",
		DisplayName:  "before",
		Status:       common.UserStatusEnabled,
		Quota:        1000,
		UsedQuota:    20,
		RequestCount: 3,
	}
	require.NoError(t, DB.Create(&user).Error)

	staleUser, err := GetUserById(user.Id, true)
	require.NoError(t, err)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":         gorm.Expr("quota - ?", 400),
		"used_quota":    gorm.Expr("used_quota + ?", 400),
		"request_count": gorm.Expr("request_count + ?", 1),
	}).Error)

	staleUser.DisplayName = "after"
	require.NoError(t, staleUser.Update(false))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, "after", got.DisplayName)
	assert.Equal(t, 600, got.Quota)
	assert.Equal(t, 420, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
}

func TestDecreaseUserQuotaImmediatelyPersistsBeforeInvalidatingCache(t *testing.T) {
	setupUserUpdateTestState(t)
	server := useUserCacheMiniRedis(t)
	user := User{
		Id:       390,
		Username: "resolution-immediate-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    1_000,
	}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, populateUserCache(user))
	assert.True(t, server.Exists(getUserCacheKey(user.Id)))

	const callbackName = "test:fail_immediate_user_quota_update"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "User" {
			tx.AddError(errors.New("forced immediate user quota failure"))
		}
	}))
	callbackRegistered := true
	t.Cleanup(func() {
		if callbackRegistered {
			require.NoError(t, DB.Callback().Update().Remove(callbackName))
		}
	})

	require.Error(t, DecreaseUserQuotaImmediately(user.Id, 100))
	var afterFailure User
	require.NoError(t, DB.First(&afterFailure, user.Id).Error)
	assert.Equal(t, 1_000, afterFailure.Quota)
	cached, err := cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 1_000, cached.Quota)

	require.NoError(t, DB.Callback().Update().Remove(callbackName))
	callbackRegistered = false
	require.NoError(t, DecreaseUserQuotaImmediately(user.Id, 100))
	var afterSuccess User
	require.NoError(t, DB.First(&afterSuccess, user.Id).Error)
	assert.Equal(t, 900, afterSuccess.Quota)
	assert.False(t, server.Exists(getUserCacheKey(user.Id)))
}

func TestDecreaseUserQuotaImmediatelyAtomicallyRejectsConcurrentOverdraft(t *testing.T) {
	setupUserUpdateTestState(t)
	user := User{
		Id:       392,
		Username: "resolution-concurrent-wallet",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    100,
	}
	require.NoError(t, DB.Create(&user).Error)

	start := make(chan struct{})
	results := make(chan error, 2)
	var requests sync.WaitGroup
	for range 2 {
		requests.Add(1)
		go func() {
			defer requests.Done()
			<-start
			results <- DecreaseUserQuotaImmediately(user.Id, 80)
		}()
	}
	close(start)
	requests.Wait()
	close(results)

	var successCount, insufficientCount int
	for err := range results {
		if err == nil {
			successCount++
			continue
		}
		if errors.Is(err, ErrInsufficientUserQuota) {
			insufficientCount++
		}
	}
	assert.Equal(t, 1, successCount)
	assert.Equal(t, 1, insufficientCount)
	var persisted User
	require.NoError(t, DB.First(&persisted, user.Id).Error)
	assert.Equal(t, 20, persisted.Quota)
}

func TestImmediateQuotaUpdatesRollBackWhenCacheInvalidationFails(t *testing.T) {
	t.Run("user", func(t *testing.T) {
		setupUserUpdateTestState(t)
		server := useUserCacheMiniRedis(t)
		user := User{Id: 391, Username: "resolution-user-cache-failure", Password: "password", Status: common.UserStatusEnabled, Quota: 1_000}
		require.NoError(t, DB.Create(&user).Error)
		require.NoError(t, populateUserCache(user))
		server.Close()

		require.Error(t, DecreaseUserQuotaImmediately(user.Id, 100))
		var persisted User
		require.NoError(t, DB.First(&persisted, user.Id).Error)
		assert.Equal(t, 1_000, persisted.Quota)
	})

	t.Run("token", func(t *testing.T) {
		setupUserUpdateTestState(t)
		server := useUserCacheMiniRedis(t)
		token := Token{Id: 391, UserId: 391, Key: "sk-resolution-token-cache-failure", Name: "resolution", Status: common.TokenStatusEnabled, RemainQuota: 1_000}
		require.NoError(t, DB.Create(&token).Error)
		require.NoError(t, cacheSetToken(token))
		server.Close()

		require.Error(t, DecreaseTokenQuotaImmediately(token.Id, token.Key, 100))
		var persisted Token
		require.NoError(t, DB.First(&persisted, token.Id).Error)
		assert.Equal(t, 1_000, persisted.RemainQuota)
		assert.Zero(t, persisted.UsedQuota)
	})
}

func TestUpdateUserSettingOnlyUpdatesSetting(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:           2,
		Username:     "setting-user",
		Password:     "password",
		Status:       common.UserStatusEnabled,
		Quota:        1000,
		UsedQuota:    20,
		RequestCount: 3,
	}
	require.NoError(t, DB.Create(&user).Error)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":         gorm.Expr("quota - ?", 250),
		"used_quota":    gorm.Expr("used_quota + ?", 250),
		"request_count": gorm.Expr("request_count + ?", 1),
	}).Error)

	require.NoError(t, UpdateUserSetting(user.Id, dto.UserSetting{Language: "zh"}))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, 750, got.Quota)
	assert.Equal(t, 270, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
	assert.Equal(t, "zh", got.GetSetting().Language)
}

func TestEnsureEmailAvailableRejectsExistingEmailCaseInsensitive(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "existing",
		Password: "old-password",
		Email:    "Taken@Example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	err := EnsureEmailAvailable(" taken@example.COM ", 0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	user, err := GetUniqueUserByEmail("TAKEN@example.com")
	require.NoError(t, err)
	assert.Equal(t, "existing", user.Username)

	require.NoError(t, EnsureEmailAvailable("taken@example.com", user.Id))
}

func TestInsertRejectsDuplicateEmailWithoutUniqueIndex(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "existing",
		Password: "old-password",
		Email:    "taken@example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	user := &User{
		Username: "oauth-user",
		Email:    "TAKEN@example.com",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	err := user.Insert(0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	var count int64
	require.NoError(t, DB.Model(&User{}).Where("username = ?", "oauth-user").Count(&count).Error)
	assert.Zero(t, count)
}

func TestInsertKeepsBlankPasswordForPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	user := &User{
		Username: "passwordless-user",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	require.NoError(t, user.Insert(0))

	var stored User
	require.NoError(t, DB.Where("username = ?", user.Username).First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestValidateAndFillRejectsPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "passwordless-user",
		Password: "",
		Status:   common.UserStatusEnabled,
	}).Error)

	loginUser := User{
		Username: "passwordless-user",
		Password: "NewPassword123",
	}
	err := loginUser.ValidateAndFill()
	require.ErrorIs(t, err, ErrInvalidCredentials)

	var stored User
	require.NoError(t, DB.Where("username = ?", "passwordless-user").First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestResetUserPasswordByEmailRequiresSingleActiveMatch(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "duplicate-1",
		Password: "old-1",
		Email:    "legacy@example.com",
		AffCode:  "dupe1",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, DB.Create(&User{
		Username: "duplicate-2",
		Password: "old-2",
		Email:    "LEGACY@example.com",
		AffCode:  "dupe2",
		Status:   common.UserStatusEnabled,
	}).Error)

	err := ResetUserPasswordByEmail("legacy@example.com", "NewPassword123")
	require.ErrorIs(t, err, ErrEmailAmbiguous)

	var duplicates []User
	require.NoError(t, DB.Where("LOWER(email) = ?", "legacy@example.com").Order("username asc").Find(&duplicates).Error)
	require.Len(t, duplicates, 2)
	assert.Equal(t, "old-1", duplicates[0].Password)
	assert.Equal(t, "old-2", duplicates[1].Password)

	require.NoError(t, DB.Create(&User{
		Username: "unique",
		Password: "old",
		Email:    "unique@example.com",
		AffCode:  "unique",
		Status:   common.UserStatusEnabled,
	}).Error)

	require.NoError(t, ResetUserPasswordByEmail("UNIQUE@example.com", "NewPassword123"))

	var unique User
	require.NoError(t, DB.Where("username = ?", "unique").First(&unique).Error)
	assert.True(t, common.ValidatePasswordAndHash("NewPassword123", unique.Password))

	err = ResetUserPasswordByEmail("missing@example.com", "NewPassword123")
	require.True(t, errors.Is(err, ErrEmailNotFound))
}
