package storage

import (
	"errors"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type TradeRecord struct {
	ID              uint      `gorm:"primaryKey"`
	Time            time.Time `gorm:"index"`
	IntentID        string    `gorm:"type:varchar(128)"`
	Type            string    `gorm:"type:varchar(32)"`
	PoolID          string    `gorm:"type:varchar(64)"`
	ChainID         int64     `gorm:"index"`
	TxHash          string    `gorm:"type:varchar(66)"`
	TargetTo        string    `gorm:"type:varchar(66)"` // expected contract address (from intent)
	OnchainTo       string    `gorm:"type:varchar(66)"` // read back from chain tx
	FromAddress     string    `gorm:"type:varchar(66)"` // tx sender read back from chain
	Nonce           uint64    `gorm:"index"`
	Status          string    `gorm:"type:varchar(32)"`
	Token0Amt       string    `gorm:"type:varchar(78)"` // raw amount in token0 decimals
	Token1Amt       string    `gorm:"type:varchar(78)"` // raw amount in token1 decimals
	SwapDetails     string    `gorm:"type:text"`        // JSON string for swap stats
	GasUsed         uint64    `gorm:"index"`
	EffectiveGasPrice string  `gorm:"type:varchar(78)"`
	GasCostNative   float64   // native chain token cost (ETH on EVM)
	PnL             float64
	IsSimulation    bool   `gorm:"index"`
	StrategyVersion string `gorm:"type:varchar(64)"`
	RiskMode        string `gorm:"type:varchar(16)"`
	NotionalUSD     float64
	GasCostUSD      float64 // legacy field (kept for compatibility)
}

type Store struct {
	db *gorm.DB
}

type DailyPnL struct {
	Day         time.Time `json:"day"`
	PnLUSD      float64   `json:"pnl_usd"`
	GasNative   float64   `json:"gas_native"`
	NetPnLUSD   float64   `json:"net_pnl_usd"`
	TradeCount  int64     `json:"trade_count"`
}

type PoolCostBasis struct {
	ID          uint      `gorm:"primaryKey"`
	PoolID      string    `gorm:"uniqueIndex:pool_chain"`
	ChainID     int64     `gorm:"uniqueIndex:pool_chain"`
	NotionalUSD float64
	UpdatedAt   time.Time `gorm:"index"`
}

// PoolPosition stores the last known UniV3 NFT position tokenId for a given pool.
// This is necessary because Uniswap V3 NonfungiblePositionManager is not enumerable by owner.
type PoolPosition struct {
	ID        uint      `gorm:"primaryKey"`
	PoolID    string    `gorm:"uniqueIndex:poolpos_chain"`
	ChainID   int64     `gorm:"uniqueIndex:poolpos_chain"`
	TokenID   string    `gorm:"type:varchar(78)"`
	UpdatedAt time.Time `gorm:"index"`
}

func NewStore(localPath string) (*Store, error) {
	if dsn := os.Getenv("SUPABASE_DB_URL"); dsn != "" {
		return newPostgresStore(dsn)
	}
	return newSQLiteStore(localPath)
}

func newSQLiteStore(path string) (*Store, error) {
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.New(
			log.New(os.Stdout, "", log.LstdFlags),
			logger.Config{
				SlowThreshold:             200 * time.Millisecond,
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true,
				Colorful:                  true,
			},
		),
	})
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
	}), &gorm.Config{
		Logger: logger.New(
			log.New(os.Stdout, "", log.LstdFlags),
			logger.Config{
				SlowThreshold:             200 * time.Millisecond,
				LogLevel:                  logger.Warn,
				IgnoreRecordNotFoundError: true,
				Colorful:                  true,
			},
		),
	})
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(&TradeRecord{}, &PoolCostBasis{}, &PoolPosition{}); err != nil {
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

func (s *Store) UpsertPoolPosition(poolID string, chainID int64, tokenID string) error {
	poolID = strings.TrimSpace(poolID)
	tokenID = strings.TrimSpace(tokenID)
	if poolID == "" || chainID == 0 || tokenID == "" {
		return nil
	}
	rec := PoolPosition{
		PoolID:    poolID,
		ChainID:   chainID,
		TokenID:   tokenID,
		UpdatedAt: time.Now(),
	}
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "pool_id"}, {Name: "chain_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"token_id", "updated_at"}),
	}).Create(&rec).Error
}

func (s *Store) GetPoolPositionTokenID(poolID string, chainID int64) (string, error) {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" || chainID == 0 {
		return "", nil
	}
	var rec PoolPosition
	err := s.db.Where("pool_id = ? AND chain_id = ?", poolID, chainID).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	return rec.TokenID, err
}

func (s *Store) ClearPoolPosition(poolID string, chainID int64) error {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" || chainID == 0 {
		return nil
	}
	return s.db.Where("pool_id = ? AND chain_id = ?", poolID, chainID).Delete(&PoolPosition{}).Error
}

func (s *Store) UpsertPoolCostBasis(poolID string, chainID int64, notionalUSD float64) error {
	if poolID == "" || notionalUSD <= 0 {
		return nil
	}
	rec := PoolCostBasis{
		PoolID:      poolID,
		ChainID:     chainID,
		NotionalUSD: notionalUSD,
		UpdatedAt:   time.Now(),
	}
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "pool_id"}, {Name: "chain_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"notional_usd", "updated_at"}),
	}).Create(&rec).Error
}

func (s *Store) GetPoolCostBasis(poolID string, chainID int64) (float64, error) {
	var rec PoolCostBasis
	err := s.db.Where("pool_id = ? AND chain_id = ?", poolID, chainID).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return rec.NotionalUSD, err
}

func (s *Store) ClearPoolCostBasis(poolID string, chainID int64) error {
	return s.db.Where("pool_id = ? AND chain_id = ?", poolID, chainID).Delete(&PoolCostBasis{}).Error
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

// GetDailyPnL returns daily aggregated PnL series for the last `days` days.
// Note: TradeRecord.PnL is currently 0 unless filled by executor; this is a scaffold for dashboard.
func (s *Store) GetDailyPnL(days int) ([]DailyPnL, error) {
	if days <= 0 {
		days = 30
	}
	start := time.Now().AddDate(0, 0, -days)
	rows := []struct {
		Day        time.Time
		PnLUSD     float64
		GasNative  float64
		TradeCount int64
	}{}
	err := s.db.Model(&TradeRecord{}).
		Select("date(time) as day, coalesce(sum(p_n_l),0) as pnl_usd, coalesce(sum(gas_cost_native),0) as gas_native, count(*) as trade_count").
		Where("time >= ?", start).
		Group("date(time)").
		Order("day asc").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	series := make([]DailyPnL, 0, len(rows))
	for _, r := range rows {
		series = append(series, DailyPnL{
			Day:        r.Day,
			PnLUSD:     r.PnLUSD,
			GasNative:  r.GasNative,
			NetPnLUSD:  r.PnLUSD - r.GasNative,
			TradeCount: r.TradeCount,
		})
	}
	return series, nil
}

// GetRecentPnLTrades returns trades within last `days` days ordered ascending.
func (s *Store) GetRecentPnLTrades(days int) ([]TradeRecord, error) {
	if days <= 0 {
		days = 30
	}
	start := time.Now().AddDate(0, 0, -days)
	var trades []TradeRecord
	err := s.db.Where("time >= ?", start).Order("time asc").Find(&trades).Error
	return trades, err
}

func (s *Store) UpdateTradeStatusByHash(txHash string, status string) error {
	return s.db.Model(&TradeRecord{}).
		Where("tx_hash = ?", txHash).
		Update("status", status).Error
}

func (s *Store) UpdateTradeStatusWithGas(txHash string, status string, gasCostNative float64, gasUsed uint64, effectiveGasPrice string) error {
	updates := map[string]interface{}{
		"status":            status,
		"gas_cost_native":   gasCostNative,
		"gas_used":          gasUsed,
		"effective_gas_price": effectiveGasPrice,
		// Keep legacy column in sync
		"gas_cost_usd": gasCostNative,
	}
	return s.db.Model(&TradeRecord{}).
		Where("tx_hash = ?", txHash).
		Updates(updates).Error
}

func (s *Store) UpdateTradeStatusWithGasAndChainMeta(txHash string, status string, gasCostNative float64, gasUsed uint64, effectiveGasPrice string, nonce uint64, from string, onchainTo string) error {
	// Best-effort: if we have an expected target and it mismatches onchain to, mark status.
	var rec TradeRecord
	if err := s.db.Where("tx_hash = ?", txHash).First(&rec).Error; err == nil {
		if rec.TargetTo != "" && onchainTo != "" && !strings.EqualFold(rec.TargetTo, onchainTo) {
			status = "mismatch"
		}
	}
	updates := map[string]interface{}{
		"status":              status,
		"gas_cost_native":     gasCostNative,
		"gas_used":            gasUsed,
		"effective_gas_price": effectiveGasPrice,
		"nonce":               nonce,
		"from_address":        from,
		"onchain_to":          onchainTo,
		"gas_cost_usd":        gasCostNative,
	}
	return s.db.Model(&TradeRecord{}).
		Where("tx_hash = ?", txHash).
		Updates(updates).Error
}
