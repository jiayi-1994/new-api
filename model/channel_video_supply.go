package model

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChannelVideoSupply is the upstream stock a channel can still deliver, in the
// unit of its VideoSchedulingConfig.SupplyUnit: USD × common.QuotaPerUnit, or
// a video count. It lives outside channels because Channel.Update rewrites the
// whole row and would clobber concurrent deductions.
type ChannelVideoSupply struct {
	ChannelId   int   `json:"channel_id" gorm:"primaryKey;autoIncrement:false"`
	Remaining   int64 `json:"remaining" gorm:"bigint;default:0"`
	UpdatedTime int64 `json:"updated_time" gorm:"bigint"`
}

// AddChannelVideoSupply adds delta (signed) to the channel's remaining supply
// atomically, creating the row on first use.
func AddChannelVideoSupply(channelID int, delta int64, now int64) error {
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&ChannelVideoSupply{ChannelId: channelID, UpdatedTime: now}).Error; err != nil {
		return err
	}
	return DB.Model(&ChannelVideoSupply{}).Where("channel_id = ?", channelID).Updates(map[string]any{
		"remaining":    gorm.Expr("remaining + ?", delta),
		"updated_time": now,
	}).Error
}

// GetChannelVideoSupplies reads the remaining supply of the given channels in
// one query. Channels without a row are absent from the result.
func GetChannelVideoSupplies(channelIDs []int) (map[int]int64, error) {
	result := make(map[int]int64, len(channelIDs))
	if len(channelIDs) == 0 {
		return result, nil
	}
	var rows []ChannelVideoSupply
	if err := DB.Where("channel_id IN ?", channelIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.ChannelId] = row.Remaining
	}
	return result, nil
}
