package storage

import (
	"errors"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type TradeRecord struct {
	ID              uint      `gorm:"primaryKey"`
	Time            time.Time `gorm:"index"`
	IntentID        string    `gorm:"type:varchar(128)"`
	Type            string    `gorm:"type:varchar(32)"`
	PoolID          string    `gorm:"type:varchar(64)"`
	ChainID         int64     `gorm:"index"`
	TxHash          string    `gorm:"type:varchar(66)"`
	Status          string    `gorm:"type:varchar(32)"`
	PnL             float64
	IsSimulation    bool   `gorm:"index"`
	StrategyVersion string `gorm:"type:varchar(64)"`
	RiskMode        string `gorm:"type:varchar(16)"`
	NotionalUSD     float64
	GasCostUSD      float64
}

type Store struct {
	db *gorm.DB
}

func NewStore(localPath string) (*Store, error) {
	if dsn := os.Getenv("SUPABASE_DB_URL"); dsn != "" {
		return newPostgresStore(dsn)
	}
	return newSQLiteStore(localPath)
}

func newSQLiteStore(path string) (*Store, error) {
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func newPostgresStore(dsn string) (*Store, error) {
	db, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  applyPreferSimpleProtocol(dsn),
		PreferSimpleProtocol: true,
	}), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(&TradeRecord{}); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "42P07" || pgErr.Code == "42P05" {
				return nil
			}
		}
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return err
	}
	return nil
}

func applyPreferSimpleProtocol(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	q := u.Query()
	q.Set("prefer_simple_protocol", "true")
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Store) SaveTrade(r *TradeRecord) error {
	return s.db.Create(r).Error
}

func (s *Store) GetTotalPnL(sim bool) (float64, error) {
	var total float64
	err := s.db.Model(&TradeRecord{}).
		Where("is_simulation = ?", sim).
		Select("coalesce(sum(p_n_l),0)").
		Scan(&total).Error
	return total, err
}

func (s *Store) GetRecentTrades(limit int) ([]TradeRecord, error) {
	var trades []TradeRecord
	err := s.db.Order("time desc").Limit(limit).Find(&trades).Error
	return trades, err
}

func (s *Store) UpdateTradeStatusByHash(txHash string, status string) error {
	return s.db.Model(&TradeRecord{}).
		Where("tx_hash = ?", txHash).
		Update("status", status).Error
}
