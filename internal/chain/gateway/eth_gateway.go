package gateway

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"

	"phoenix-v3/internal/chain"
	"phoenix-v3/internal/strategy"
)

type ReceiptResult struct {
	ChainID           int64
	Hash              common.Hash
	Status            TxStatus
	StatusCode        uint64
	GasUsed           uint64
	EffectiveGasPrice *big.Int
	Nonce             uint64
	From              common.Address
	To                common.Address
	RevertReason      string
}

var erc20ABI abi.ABI

func init() {
	parsed, err := abi.JSON(strings.NewReader(`[
		{
			"constant":true,"inputs":[{"name":"owner","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"type":"function"
		},
		{
			"constant":true,"inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],"name":"allowance","outputs":[{"name":"","type":"uint256"}],"type":"function"
		},
		{
			"constant":false,"inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"name":"approve","outputs":[{"name":"","type":"bool"}],"type":"function"
		}
	]`))
	if err != nil {
		panic(fmt.Sprintf("failed to parse erc20 abi: %v", err))
	}
	erc20ABI = parsed
}

type EthGateway struct {
	client    *ethclient.Client
	wallet    *chain.Wallet
	chainID   *big.Int
	receiptCh chan ReceiptResult

	gasMultiplier      float64
	maxRetries         int
	retryBackoffMs     int
	gasBumpPct         float64
	approvalMultiplier float64
	preflight          bool

	nonceMu sync.Mutex
	nonce   uint64
}

func NewEthGateway(rpcURL string, privKey string, opts ...Option) (*EthGateway, error) {
	httpClient := &http.Client{Timeout: 15 * time.Second}
	rpcClient, err := rpc.DialHTTPWithClient(rpcURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to dial rpc: %w", err)
	}
	client := ethclient.NewClient(rpcClient)

	wallet, err := chain.NewWallet(privKey)
	if err != nil {
		return nil, err
	}

	chainID, err := client.ChainID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get chain id: %w", err)
	}

	gw := &EthGateway{
		client:             client,
		wallet:             wallet,
		chainID:            chainID,
		receiptCh:          make(chan ReceiptResult, 64),
		gasMultiplier:      1.0,
		maxRetries:         3,
		retryBackoffMs:     1500,
		gasBumpPct:         0.15,
		approvalMultiplier: 1.05,
		preflight:          true,
	}
	for _, opt := range opts {
		opt(gw)
	}

	// Initialize nonce
	if err := gw.syncNonce(); err != nil {
		return nil, err
	}

	return gw, nil
}

type Option func(*EthGateway)

func WithGasMultiplier(m float64) Option {
	return func(g *EthGateway) {
		if m > 0 {
			g.gasMultiplier = m
		}
	}
}

func WithRetry(maxRetries int, backoffMs int, gasBumpPct float64) Option {
	return func(g *EthGateway) {
		if maxRetries > 0 {
			g.maxRetries = maxRetries
		}
		if backoffMs > 0 {
			g.retryBackoffMs = backoffMs
		}
		if gasBumpPct > 0 {
			g.gasBumpPct = gasBumpPct
		}
	}
}

// WithApprovalMultiplier sets allowance approval multiplier (>=1.0).
func WithApprovalMultiplier(mult float64) Option {
	return func(g *EthGateway) {
		if mult >= 1.0 {
			g.approvalMultiplier = mult
		}
	}
}

// WithPreflight toggles preflight simulation (EstimateGas) before sending.
func WithPreflight(enabled bool) Option {
	return func(g *EthGateway) {
		g.preflight = enabled
	}
}

func (g *EthGateway) syncNonce() error {
	nonce, err := g.client.PendingNonceAt(context.Background(), g.wallet.Address)
	if err != nil {
		return fmt.Errorf("failed to get nonce: %w", err)
	}
	g.nonce = nonce
	return nil
}

func (g *EthGateway) Address() string {
	return g.wallet.Address.Hex()
}

func (g *EthGateway) Receipts() <-chan ReceiptResult {
	return g.receiptCh
}

func (g *EthGateway) WalletAddress() common.Address {
	return g.wallet.Address
}

func (g *EthGateway) ChainID() *big.Int {
	return new(big.Int).Set(g.chainID)
}

func (g *EthGateway) Call(ctx context.Context, to common.Address, data []byte) ([]byte, error) {
	call := ethereum.CallMsg{To: &to, Data: data}
	return g.client.CallContract(ctx, call, nil)
}

// TxReceipt fetches a transaction receipt by hash.
// It is safe to call repeatedly for polling ("not found" until mined).
func (g *EthGateway) TxReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	return g.client.TransactionReceipt(ctx, hash)
}

func (g *EthGateway) BalanceOfERC20(ctx context.Context, token common.Address) (*big.Int, error) {
	data, err := erc20ABI.Pack("balanceOf", g.wallet.Address)
	if err != nil {
		return nil, fmt.Errorf("pack balanceOf failed: %w", err)
	}
	call := ethereum.CallMsg{To: &token, Data: data}
	res, err := g.client.CallContract(ctx, call, nil)
	if err != nil {
		return nil, fmt.Errorf("call balanceOf failed: %w", err)
	}
	values, err := erc20ABI.Unpack("balanceOf", res)
	if err != nil || len(values) == 0 {
		return nil, fmt.Errorf("unpack balanceOf failed: %w", err)
	}
	bal, ok := values[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected balance type %T", values[0])
	}
	return bal, nil
}

// EnsureAllowanceTx checks if spender has enough allowance, otherwise sends an Approve tx.
// Returns (txHash, minedReceipt, err). If allowance is already sufficient, returns zero hash and nil receipt.
func (g *EthGateway) EnsureAllowanceTx(ctx context.Context, token, spender common.Address, amount *big.Int) (common.Hash, *types.Receipt, error) {
	// 1. Check Allowance
	allowanceData, err := erc20ABI.Pack("allowance", g.wallet.Address, spender)
	if err != nil {
		return common.Hash{}, nil, fmt.Errorf("pack allowance failed: %w", err)
	}
	call := ethereum.CallMsg{To: &token, Data: allowanceData}
	res, err := g.client.CallContract(ctx, call, nil)
	if err != nil {
		return common.Hash{}, nil, fmt.Errorf("call allowance failed: %w", err)
	}
	values, err := erc20ABI.Unpack("allowance", res)
	if err != nil || len(values) == 0 {
		return common.Hash{}, nil, fmt.Errorf("unpack allowance failed: %w", err)
	}
	currentAllowance, ok := values[0].(*big.Int)
	if !ok {
		return common.Hash{}, nil, fmt.Errorf("unexpected allowance type %T", values[0])
	}
	log.Printf("[Gateway Debug] Checked Allowance: Token=%s Spender=%s Amt=%s Current=%s", token.Hex(), spender.Hex(), amount.String(), currentAllowance.String())

	if currentAllowance.Cmp(amount) >= 0 {
		return common.Hash{}, nil, nil // Already sufficient
	}

	log.Printf("[Gateway] Approving %s for %s", token.Hex(), spender.Hex())
	approveAmt := new(big.Int).Set(amount)
	if approveAmt == nil || approveAmt.Sign() <= 0 {
		approveAmt = big.NewInt(0)
	}
	if g.approvalMultiplier > 1.0 && approveAmt.Sign() > 0 {
		mul := big.NewFloat(g.approvalMultiplier)
		scaled, _ := new(big.Float).Mul(new(big.Float).SetInt(approveAmt), mul).Int(nil)
		if scaled != nil && scaled.Sign() > 0 {
			approveAmt = scaled
		}
	}

	approveData, err := erc20ABI.Pack("approve", spender, approveAmt)
	if err != nil {
		return common.Hash{}, nil, fmt.Errorf("pack approve failed: %w", err)
	}

	approveCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	g.nonceMu.Lock()
	defer g.nonceMu.Unlock()

	nonce := g.nonce
	var lastHash common.Hash
	var lastErr error
	for attempt := 0; attempt < g.maxRetries; attempt++ {
		gasPrice, err := g.client.SuggestGasPrice(approveCtx)
		if err != nil {
			lastErr = err
			continue
		}

		adjPrice := new(big.Int).Set(gasPrice)
		if g.gasMultiplier != 1.0 {
			mul := big.NewFloat(g.gasMultiplier)
			f, _ := new(big.Float).SetInt(adjPrice).Mul(new(big.Float).SetInt(adjPrice), mul).Int(nil)
			adjPrice = f
		}
		if attempt > 0 {
			// Use a larger bump for replacements to satisfy "replacement transaction underpriced".
			bump := big.NewFloat(1 + (g.gasBumpPct+0.20)*float64(attempt))
			f, _ := new(big.Float).SetInt(adjPrice).Mul(new(big.Float).SetInt(adjPrice), bump).Int(nil)
			adjPrice = f
		}

		tx := types.NewTransaction(nonce, token, big.NewInt(0), 100000, adjPrice, approveData)
		signedTx, err := types.SignTx(tx, types.NewEIP155Signer(g.chainID), g.wallet.PrivateKey)
		if err != nil {
			return common.Hash{}, nil, err
		}

		if err := g.client.SendTransaction(approveCtx, signedTx); err != nil {
			lastErr = err
			msg := err.Error()
			if strings.Contains(msg, "replacement transaction underpriced") {
				continue
			}
			if strings.Contains(msg, "nonce") {
				if err := g.syncNonce(); err == nil {
					nonce = g.nonce
				}
			}
			backoff := time.Duration(g.retryBackoffMs*(attempt+1)) * time.Millisecond
			select {
			case <-approveCtx.Done():
				return common.Hash{}, nil, fmt.Errorf("approve tx context done: %w", lastErr)
			case <-time.After(backoff):
			}
			continue
		}

		lastHash = signedTx.Hash()
		log.Printf("[Gateway] Sent Approve Tx: %s", lastHash.Hex())
		if nonce >= g.nonce {
			g.nonce = nonce + 1
		}

		// Wait for approve to be mined before proceeding. Otherwise the next tx (e.g. UniV3 mint)
		// can revert with "STF" due to allowance not yet updated.
		mineWait, mineCancel := context.WithTimeout(approveCtx, 60*time.Second)
		for {
			receipt, err := g.client.TransactionReceipt(mineWait, lastHash)
			if err == nil && receipt != nil {
				mineCancel()
				go g.waitReceipt(lastHash)
				return lastHash, receipt, nil
			}
			select {
			case <-mineWait.Done():
				mineCancel()
				break
			case <-time.After(2 * time.Second):
			}
		}
		// Not mined yet; retry with a replacement tx using same nonce and higher gas.
	}
	if lastHash != (common.Hash{}) {
		return lastHash, nil, fmt.Errorf("approve tx not mined: %s", lastHash.Hex())
	}
	return common.Hash{}, nil, fmt.Errorf("approve tx failed: %w", lastErr)
}

// EnsureAllowance checks if spender has enough allowance, otherwise sends Approve tx.
// It is kept for backward compatibility; prefer EnsureAllowanceTx when you need the tx hash/receipt.
func (g *EthGateway) EnsureAllowance(ctx context.Context, token, spender common.Address, amount *big.Int) error {
	_, _, err := g.EnsureAllowanceTx(ctx, token, spender, amount)
	return err
}

func (g *EthGateway) Send(ctx context.Context, intent strategy.Intent) (*TxResult, error) {
	// Build call once.
	var callData []byte
	if hexData := intent.Metadata["calldata"]; hexData != "" {
		if data, err := hex.DecodeString(strings.TrimPrefix(hexData, "0x")); err == nil {
			callData = data
		}
	}
	toMeta := intent.Metadata["target"]
	if strings.TrimSpace(toMeta) == "" {
		return nil, fmt.Errorf("gateway: intent target required (intent=%s)", intent.ID)
	}
	toAddr := common.HexToAddress(toMeta)
	if toAddr == (common.Address{}) {
		return nil, fmt.Errorf("gateway: zero target address (intent=%s)", intent.ID)
	}
	if len(callData) == 0 {
		return nil, fmt.Errorf("gateway: calldata required (intent=%s)", intent.ID)
	}
	value := big.NewInt(0)
	if valStr := intent.Metadata["value"]; valStr != "" {
		if val, ok := new(big.Int).SetString(valStr, 10); ok {
			value = val
		}
	}

	if g.preflight {
		msg := ethereum.CallMsg{
			From:  g.wallet.Address,
			To:    &toAddr,
			Value: value,
			Data:  callData,
		}
		if _, err := g.client.EstimateGas(ctx, msg); err != nil {
			return nil, fmt.Errorf("gateway: preflight estimateGas failed (intent=%s to=%s): %w", intent.ID, toAddr.Hex(), err)
		}
	}

	g.nonceMu.Lock()
	defer g.nonceMu.Unlock()

	nonce := g.nonce
	var lastErr error
	for attempt := 0; attempt < g.maxRetries; attempt++ {

		gasPrice, err := g.client.SuggestGasPrice(ctx)
		if err != nil {
			lastErr = fmt.Errorf("gas price error: %w", err)
			continue
		}
		// Apply multiplier and bump on retries.
		adjPrice := new(big.Int).Set(gasPrice)
		if g.gasMultiplier != 1.0 {
			mul := big.NewFloat(g.gasMultiplier)
			f, _ := new(big.Float).SetInt(adjPrice).Mul(new(big.Float).SetInt(adjPrice), mul).Int(nil)
			adjPrice = f
		}
		if attempt > 0 {
			bump := big.NewFloat(1 + g.gasBumpPct*float64(attempt))
			f, _ := new(big.Float).SetInt(adjPrice).Mul(new(big.Float).SetInt(adjPrice), bump).Int(nil)
			adjPrice = f
		}

		tx := types.NewTransaction(nonce, toAddr, value, 800000, adjPrice, callData)
		signedTx, err := types.SignTx(tx, types.NewEIP155Signer(g.chainID), g.wallet.PrivateKey)
		if err != nil {
			lastErr = fmt.Errorf("failed to sign tx: %w", err)
			continue
		}

		if err := g.client.SendTransaction(ctx, signedTx); err != nil {
			msg := err.Error()
			lastErr = fmt.Errorf("send tx failed: %w", err)
			// On nonce related errors, re-sync nonce and retry with the synced nonce.
			if strings.Contains(msg, "nonce") {
				if err := g.syncNonce(); err == nil {
					nonce = g.nonce
				}
			}
			// Replacement errors retry with the same nonce but higher gas.
			if strings.Contains(msg, "replacement transaction underpriced") {
				// keep nonce
			}
			backoff := time.Duration(g.retryBackoffMs*(attempt+1)) * time.Millisecond
			log.Printf("[Gateway] Send attempt %d failed: %v (backoff %s)", attempt+1, err, backoff)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
				continue
			}
		}

		log.Printf("[Gateway] Sent Intent %s Tx=%s Nonce=%d GasPrice=%s", intent.ID, signedTx.Hash().Hex(), nonce, adjPrice.String())
		if nonce >= g.nonce {
			g.nonce = nonce + 1
		}
		go g.waitReceipt(signedTx.Hash())
		return &TxResult{Hash: signedTx.Hash(), Status: StatusPending}, nil
	}

	_ = g.syncNonce()
	return nil, lastErr
}

func (g *EthGateway) waitReceipt(hash common.Hash) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var nonce uint64
	var fromAddr common.Address
	var toAddr common.Address
	// Best-effort read tx details once.
	if tx, _, err := g.client.TransactionByHash(ctx, hash); err == nil && tx != nil {
		nonce = tx.Nonce()
		from, err := types.Sender(types.NewEIP155Signer(g.chainID), tx)
		if err == nil {
			fromAddr = from
		}
		if tx.To() != nil {
			toAddr = *tx.To()
		}
	}

	for {
		receipt, err := g.client.TransactionReceipt(ctx, hash)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				log.Printf("[Gateway] wait receipt canceled: %v", err)
				return
			}
			if err.Error() == "not found" {
				time.Sleep(3 * time.Second)
				continue
			}
			time.Sleep(3 * time.Second)
			continue
		}
		log.Printf("[Gateway] Tx %s status=%d gasUsed=%d", hash.Hex(), receipt.Status, receipt.GasUsed)
		var txStatus TxStatus
		if receipt.Status == 1 {
			txStatus = StatusMined
		} else {
			txStatus = StatusReverted
		}
		revertReason := ""
		if receipt.Status != 1 {
			revertReason = g.tryFetchRevertReason(hash, receipt.BlockNumber)
		}
		gasPrice := receipt.EffectiveGasPrice
		if gasPrice == nil {
			if tx, _, err := g.client.TransactionByHash(ctx, hash); err == nil {
				gasPrice = tx.GasPrice()
			}
		}
		select {
		case g.receiptCh <- ReceiptResult{
			ChainID:           g.chainID.Int64(),
			Hash:              hash,
			Status:            txStatus,
			StatusCode:        receipt.Status,
			GasUsed:           receipt.GasUsed,
			EffectiveGasPrice: gasPrice,
			Nonce:             nonce,
			From:              fromAddr,
			To:                toAddr,
			RevertReason:      revertReason,
		}:
		default:
			log.Printf("[Gateway] receipt channel full, drop %s", hash.Hex())
		}
		return
	}
}

func (g *EthGateway) tryFetchRevertReason(hash common.Hash, blockNumber *big.Int) string {
	if g == nil || g.client == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, _, err := g.client.TransactionByHash(ctx, hash)
	if err != nil || tx == nil || tx.To() == nil {
		return ""
	}
	msg := ethereum.CallMsg{
		From:  g.wallet.Address,
		To:    tx.To(),
		Value: tx.Value(),
		Data:  tx.Data(),
	}
	_, callErr := g.client.CallContract(ctx, msg, blockNumber)
	if callErr == nil {
		return ""
	}
	s := callErr.Error()
	if i := strings.Index(s, "execution reverted:"); i >= 0 {
		return strings.TrimSpace(strings.TrimPrefix(s[i:], "execution reverted:"))
	}
	if strings.Contains(s, "execution reverted") {
		return "execution reverted"
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
