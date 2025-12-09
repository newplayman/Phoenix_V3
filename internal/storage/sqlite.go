package storage

import (
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type TradeRecord struct {
	ID           uint      `gorm:"primaryKey"`
	Time         time.Time `gorm:"index"`
	IntentID     string    `gorm:"type:varchar(64)"`
	Type         string    `gorm:"type:varchar(32)"`
	PoolID       string    `gorm:"type:varchar(64)"`
	TxHash       string    `gorm:"type:varchar(66)"`
	Status       string    `gorm:"type:varchar(32)"`
	PnL          float64   // Estimated or realized PnL
	IsSimulation bool      `gorm:"index"` // true for dry-run
}

type Store struct {
	db *gorm.DB
}

func NewStore(dbPath string) (*Store, error) {
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// Migrate schema
	if err := db.AutoMigrate(&TradeRecord{}); err != nil {
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) SaveTrade(r *TradeRecord) error {
	return s.db.Create(r).Error
}

func (s *Store) GetTotalPnL(sim bool) (float64, error) {
	var total float64
	err := s.db.Model(&TradeRecord{}).
		Where("is_simulation = ?", sim).
		Select("sum(p_n_l)").
		Scan(&total).Error
	return total, err
}

func (s *Store) GetRecentTrades(limit int) ([]TradeRecord, error) {
	var trades []TradeRecord
	err := s.db.Order("time desc").Limit(limit).Find(&trades).Error
	return trades, err
}
