package model

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChannelVideoSupply is the upstream stock a channel can still deliver.
// Remaining is in Unit: USD × common.QuotaPerUnit for "usd", videos for
// "count". It lives outside channels because Channel.Update rewrites the whole
// row and would clobber concurrent deductions.
type ChannelVideoSupply struct {
	ChannelId   int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false"`
	Unit        string `json:"unit" gorm:"type:varchar(8);default:''"`
	Remaining   int64  `json:"remaining" gorm:"bigint;default:0"`
	UpdatedTime int64  `json:"updated_time" gorm:"bigint"`
}

// AddChannelVideoSupply adds delta (signed) to the channel's remaining supply
// atomically, creating the row on first use. A row kept in another unit is
// restarted from delta: a unit switch never reinterprets the old number.
func AddChannelVideoSupply(channelID int, unit string, delta int64, now int64) error {
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&ChannelVideoSupply{ChannelId: channelID, Unit: unit, UpdatedTime: now}).Error; err != nil {
		return err
	}
	return DB.Model(&ChannelVideoSupply{}).Where("channel_id = ?", channelID).Updates(map[string]any{
		"remaining":    gorm.Expr("CASE WHEN unit = ? THEN remaining + ? ELSE ? END", unit, delta, delta),
		"unit":         unit,
		"updated_time": now,
	}).Error
}

// ConsumeChannelVideoSupply subtracts amount from the stock kept in unit. It
// reports false when the stock is kept in another unit, so a task frozen in
// the old unit never spends the new balance.
func ConsumeChannelVideoSupply(channelID int, unit string, amount int64, now int64) (bool, error) {
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&ChannelVideoSupply{ChannelId: channelID, Unit: unit, UpdatedTime: now}).Error; err != nil {
		return false, err
	}
	result := DB.Model(&ChannelVideoSupply{}).Where("channel_id = ? AND unit = ?", channelID, unit).Updates(map[string]any{
		"remaining":    gorm.Expr("remaining - ?", amount),
		"updated_time": now,
	})
	return result.RowsAffected > 0, result.Error
}

// GetChannelVideoSupplies reads the stock rows of the given channels in one
// query. Channels without a row are absent from the result.
func GetChannelVideoSupplies(channelIDs []int) (map[int]ChannelVideoSupply, error) {
	result := make(map[int]ChannelVideoSupply, len(channelIDs))
	if len(channelIDs) == 0 {
		return result, nil
	}
	var rows []ChannelVideoSupply
	if err := DB.Where("channel_id IN ?", channelIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ChannelId] = row
	}
	return result, nil
}

// VideoSupplyRemaining is the usable balance of a stock row read for unit:
// a row kept in another unit counts as empty until it is topped up again.
func VideoSupplyRemaining(row ChannelVideoSupply, found bool, unit string) int64 {
	if !found || row.Unit != unit {
		return 0
	}
	return row.Remaining
}
