package storage

import (
	"strings"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Operation tracks a console action from preview -> execute -> completion.
// It is used for idempotency and for persisting the preview plan.
type Operation struct {
	ID             uint           `gorm:"primaryKey" json:"id"`
	OperationID    string         `gorm:"uniqueIndex;type:varchar(80)" json:"operation_id"`
	Actor          string         `gorm:"index;type:varchar(64)" json:"actor"`
	ActionType     string         `gorm:"index;type:varchar(64)" json:"action_type"`
	PoolID         string         `gorm:"index;type:varchar(64)" json:"pool_id"`
	ChainID        int64          `gorm:"index" json:"chain_id"`
	Status         string         `gorm:"index;type:varchar(32)" json:"status"`
	IdempotencyKey string         `gorm:"index;type:varchar(128)" json:"idempotency_key"`
	Preview        datatypes.JSON `json:"preview"`
	Warnings       datatypes.JSON `json:"warnings"`
	ExpiresAt      time.Time      `gorm:"index" json:"expires_at"`
	IntentID       string         `gorm:"index;type:varchar(128)" json:"intent_id"`
	Reason         string         `gorm:"type:text" json:"reason"`
	CreatedAt      time.Time      `gorm:"index" json:"created_at"`
	UpdatedAt      time.Time      `gorm:"index" json:"updated_at"`
}

type OperatorAction struct {
	ID         uint           `gorm:"primaryKey" json:"id"`
	TS         time.Time      `gorm:"index" json:"ts"`
	Actor      string         `gorm:"type:varchar(64)" json:"actor"`
	ActionType string         `gorm:"index;type:varchar(64)" json:"action_type"`
	PoolID     string         `gorm:"index;type:varchar(64)" json:"pool_id"`
	ChainID    int64          `gorm:"index" json:"chain_id"`
	Request    datatypes.JSON `json:"request"`
	Result     datatypes.JSON `json:"result"`
}

type IntentRecord struct {
	ID              uint           `gorm:"primaryKey" json:"id"`
	IntentID        string         `gorm:"uniqueIndex;type:varchar(128)" json:"intent_id"`
	PoolID          string         `gorm:"index;type:varchar(64)" json:"pool_id"`
	ChainID         int64          `gorm:"index" json:"chain_id"`
	Type            string         `gorm:"type:varchar(32)" json:"type"`
	Status          string         `gorm:"index;type:varchar(32)" json:"status"`
	RiskMode        string         `gorm:"type:varchar(16)" json:"risk_mode"`
	StrategyVersion string         `gorm:"type:varchar(64)" json:"strategy_version"`
	Metadata        datatypes.JSON `json:"metadata"`
	CreatedAt       time.Time      `gorm:"index" json:"created_at"`
	UpdatedAt       time.Time      `gorm:"index" json:"updated_at"`
}

type IntentStepRecord struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	IntentID  string         `gorm:"index;type:varchar(128)" json:"intent_id"`
	StepType  string         `gorm:"type:varchar(32)" json:"step_type"`
	StepIndex int            `gorm:"index" json:"step_index"`
	Status    string         `gorm:"index;type:varchar(16)" json:"status"`
	TxHash    string         `gorm:"type:varchar(66)" json:"tx_hash"`
	Details   datatypes.JSON `json:"details"`
	UpdatedAt time.Time      `gorm:"index" json:"updated_at"`
}

type TxReceiptRecord struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	ChainID           int64     `gorm:"index" json:"chain_id"`
	TxHash            string    `gorm:"uniqueIndex;type:varchar(66)" json:"tx_hash"`
	Nonce             uint64    `gorm:"index" json:"nonce"`
	FromAddr          string    `gorm:"type:varchar(66)" json:"from_addr"`
	ToAddr            string    `gorm:"type:varchar(66)" json:"to_addr"`
	Status            uint64    `gorm:"index" json:"status"`
	GasUsed           uint64    `gorm:"index" json:"gas_used"`
	EffectiveGasPrice string    `gorm:"type:varchar(78)" json:"effective_gas_price"`
	RevertReason      string    `gorm:"type:text" json:"revert_reason"`
	MinedAt           time.Time `gorm:"index" json:"mined_at"`
}

type PoolSnapshot struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	PoolID          string    `gorm:"index;type:varchar(64)" json:"pool_id"`
	ChainID         int64     `gorm:"index" json:"chain_id"`
	TS              time.Time `gorm:"index" json:"ts"`
	DexTick         int64     `json:"dex_tick"`
	DexPrice        float64   `json:"dex_price"`
	Liquidity       string    `gorm:"type:varchar(78)" json:"liquidity"`
	PositionTokenID string    `gorm:"type:varchar(78)" json:"position_token_id"`
	PosTickLower    int64     `json:"pos_tick_lower"`
	PosTickUpper    int64     `json:"pos_tick_upper"`
	PosLiquidity    string    `gorm:"type:varchar(78)" json:"pos_liquidity"`
	SigmaDaily      float64   `json:"sigma_daily"`
	WidthPct        float64   `json:"width_pct"`
	Profile         string    `gorm:"type:varchar(16)" json:"profile"`
}

type BotHeartbeat struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	BotID       string    `gorm:"index;type:varchar(64)" json:"bot_id"`
	ChainID     int64     `gorm:"index" json:"chain_id"`
	TS          time.Time `gorm:"index" json:"ts"`
	LatestBlock uint64    `json:"latest_block"`
	QueueDepth  int       `json:"queue_depth"`
	RiskMode    string    `gorm:"type:varchar(16)" json:"risk_mode"`
	RPCURLHash  string    `gorm:"type:varchar(128)" json:"rpc_url_hash"`
}

func (s *Store) UpsertOperationPreview(op *Operation) (*Operation, error) {
	if s == nil || s.db == nil || op == nil {
		return nil, nil
	}
	op.OperationID = strings.TrimSpace(op.OperationID)
	op.Actor = strings.TrimSpace(op.Actor)
	op.ActionType = strings.TrimSpace(op.ActionType)
	op.PoolID = strings.TrimSpace(op.PoolID)
	op.IdempotencyKey = strings.TrimSpace(op.IdempotencyKey)
	if op.CreatedAt.IsZero() {
		op.CreatedAt = time.Now()
	}
	op.UpdatedAt = time.Now()

	if op.IdempotencyKey != "" {
		var existing Operation
		err := s.db.Where("actor = ? AND idempotency_key = ? AND action_type = ?", op.Actor, op.IdempotencyKey, op.ActionType).
			Order("id desc").
			First(&existing).Error
		if err == nil {
			// If not expired, return existing.
			if existing.ExpiresAt.IsZero() || time.Now().Before(existing.ExpiresAt) {
				return &existing, nil
			}
		} else if err != nil && err != gorm.ErrRecordNotFound {
			return nil, err
		}
	}
	if err := s.db.Create(op).Error; err != nil {
		return nil, err
	}
	return op, nil
}

func (s *Store) GetOperationByOperationID(operationID string) (*Operation, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var op Operation
	err := s.db.Where("operation_id = ?", strings.TrimSpace(operationID)).First(&op).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &op, err
}

func (s *Store) UpdateOperationExecute(operationID string, status string, reason string, intentID string) error {
	if s == nil || s.db == nil {
		return nil
	}
	updates := map[string]interface{}{
		"status":     status,
		"reason":     reason,
		"intent_id":  intentID,
		"updated_at": time.Now(),
	}
	return s.db.Model(&Operation{}).Where("operation_id = ?", strings.TrimSpace(operationID)).Updates(updates).Error
}

func (s *Store) CreateOperatorAction(a *OperatorAction) error {
	if s == nil || s.db == nil || a == nil {
		return nil
	}
	if a.TS.IsZero() {
		a.TS = time.Now()
	}
	return s.db.Create(a).Error
}

func (s *Store) UpsertIntentRecord(rec *IntentRecord) error {
	if s == nil || s.db == nil || rec == nil {
		return nil
	}
	rec.IntentID = strings.TrimSpace(rec.IntentID)
	if rec.IntentID == "" {
		return nil
	}
	rec.PoolID = strings.TrimSpace(rec.PoolID)
	now := time.Now()
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = now
	}
	rec.UpdatedAt = now

	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "intent_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"pool_id", "chain_id", "type", "status", "risk_mode", "strategy_version", "metadata", "updated_at"}),
	}).Create(rec).Error
}

func (s *Store) CreateIntentStep(step *IntentStepRecord) error {
	if s == nil || s.db == nil || step == nil {
		return nil
	}
	step.IntentID = strings.TrimSpace(step.IntentID)
	if step.IntentID == "" {
		return nil
	}
	step.UpdatedAt = time.Now()
	return s.db.Create(step).Error
}

func (s *Store) UpdateIntentStep(intentID string, stepIndex int, status string, txHash string, details datatypes.JSON) error {
	if s == nil || s.db == nil {
		return nil
	}
	updates := map[string]interface{}{
		"status":     status,
		"tx_hash":    txHash,
		"details":    details,
		"updated_at": time.Now(),
	}
	return s.db.Model(&IntentStepRecord{}).
		Where("intent_id = ? AND step_index = ?", strings.TrimSpace(intentID), stepIndex).
		Updates(updates).Error
}

func (s *Store) UpsertTxReceipt(rec *TxReceiptRecord) error {
	if s == nil || s.db == nil || rec == nil {
		return nil
	}
	rec.TxHash = strings.TrimSpace(rec.TxHash)
	if rec.TxHash == "" {
		return nil
	}
	if rec.MinedAt.IsZero() {
		rec.MinedAt = time.Now()
	}
	return s.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "tx_hash"}},
		DoUpdates: clause.AssignmentColumns([]string{"chain_id", "nonce", "from_addr", "to_addr", "status", "gas_used", "effective_gas_price", "revert_reason", "mined_at"}),
	}).Create(rec).Error
}

func (s *Store) ListIntents(poolID string, status string, limit int, cursorID uint) ([]IntentRecord, uint, error) {
	if s == nil || s.db == nil {
		return nil, 0, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := s.db.Model(&IntentRecord{}).Order("id desc").Limit(limit)
	if cursorID > 0 {
		q = q.Where("id < ?", cursorID)
	}
	if poolID = strings.TrimSpace(poolID); poolID != "" {
		q = q.Where("pool_id = ?", poolID)
	}
	if status = strings.TrimSpace(status); status != "" {
		q = q.Where("status = ?", status)
	}
	var out []IntentRecord
	if err := q.Find(&out).Error; err != nil {
		return nil, 0, err
	}
	next := uint(0)
	if len(out) == limit {
		next = out[len(out)-1].ID
	}
	return out, next, nil
}

func (s *Store) GetIntentWithSteps(intentID string) (*IntentRecord, []IntentStepRecord, error) {
	if s == nil || s.db == nil {
		return nil, nil, nil
	}
	intentID = strings.TrimSpace(intentID)
	if intentID == "" {
		return nil, nil, nil
	}
	var rec IntentRecord
	if err := s.db.Where("intent_id = ?", intentID).First(&rec).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var steps []IntentStepRecord
	_ = s.db.Where("intent_id = ?", intentID).Order("step_index asc").Find(&steps).Error
	return &rec, steps, nil
}

func (s *Store) ListOperatorActions(poolID string, actionType string, limit int, cursorID uint) ([]OperatorAction, uint, error) {
	if s == nil || s.db == nil {
		return nil, 0, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := s.db.Model(&OperatorAction{}).Order("id desc").Limit(limit)
	if cursorID > 0 {
		q = q.Where("id < ?", cursorID)
	}
	if poolID = strings.TrimSpace(poolID); poolID != "" {
		q = q.Where("pool_id = ?", poolID)
	}
	if actionType = strings.TrimSpace(actionType); actionType != "" {
		q = q.Where("action_type = ?", actionType)
	}
	var out []OperatorAction
	if err := q.Find(&out).Error; err != nil {
		return nil, 0, err
	}
	next := uint(0)
	if len(out) == limit {
		next = out[len(out)-1].ID
	}
	return out, next, nil
}
