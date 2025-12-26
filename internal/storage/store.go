package storage

import (
	"encoding/json"
	"errors"
	"math/big"
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
	MetaJSON        string    `gorm:"type:text"`
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

func (s *Store) GetTradesSince(start time.Time) ([]TradeRecord, error) {
	var trades []TradeRecord
	err := s.db.Where("time >= ?", start).Order("time desc").Find(&trades).Error
	return trades, err
}

func (s *Store) UpdateTradeStatusByHash(txHash string, status string) error {
	return s.db.Model(&TradeRecord{}).
		Where("tx_hash = ?", txHash).
		Update("status", status).Error
}

func (s *Store) UpdateTradeReceiptByHash(txHash string, status string, gasUsed uint64, gasPriceWei *big.Int) error {
	if txHash == "" {
		return nil
	}
	var tr TradeRecord
	if err := s.db.Where("tx_hash = ?", txHash).First(&tr).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	meta := make(map[string]any)
	if tr.MetaJSON != "" {
		_ = json.Unmarshal([]byte(tr.MetaJSON), &meta)
	}
	meta["receipt_status"] = status
	meta["gas_used"] = gasUsed
	if gasPriceWei != nil && gasPriceWei.Sign() >= 0 {
		meta["gas_price_wei"] = gasPriceWei.String()
		if gasUsed > 0 && gasPriceWei.Sign() > 0 {
			gasCostWei := new(big.Int).Mul(new(big.Int).SetUint64(gasUsed), gasPriceWei)
			meta["gas_cost_wei"] = gasCostWei.String()
		}
	}
	meta["receipt_updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	b, _ := json.Marshal(meta)

	return s.db.Model(&TradeRecord{}).
		Where("id = ?", tr.ID).
		Updates(map[string]any{
			"status":    status,
			"meta_json": string(b),
		}).Error
}
