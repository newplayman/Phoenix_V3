package gateway

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"phoenix-v3/internal/chain"
	"phoenix-v3/internal/contracts"
)

type ReceiptResult struct {
	Hash        common.Hash
	Status      TxStatus
	GasUsed     uint64
	GasPriceWei *big.Int
}

var erc20ABI abi.ABI

func init() {
	parsed, err := abi.JSON(strings.NewReader(`[
		{
			"constant":true,
			"inputs":[{"name":"owner","type":"address"}],
			"name":"balanceOf",
			"outputs":[{"name":"","type":"uint256"}],
			"stateMutability":"view",
			"type":"function"
		},
		{
			"constant":true,
			"inputs":[{"name":"owner","type":"address"},{"name":"spender","type":"address"}],
			"name":"allowance",
			"outputs":[{"name":"","type":"uint256"}],
			"stateMutability":"view",
			"type":"function"
		},
		{
			"constant":false,
			"inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],
			"name":"approve",
			"outputs":[{"name":"","type":"bool"}],
			"stateMutability":"nonpayable",
			"type":"function"
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

	nonceMu sync.Mutex
	nonce   uint64
}

func NewEthGateway(rpcURL string, privKey string) (*EthGateway, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to dial rpc: %w", err)
	}

	wallet, err := chain.NewWallet(privKey)
	if err != nil {
		return nil, err
	}

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get chain id: %w", err)
	}

	gw := &EthGateway{
		client:    client,
		wallet:    wallet,
		chainID:   chainID,
		receiptCh: make(chan ReceiptResult, 64),
	}

	// Initialize nonce
	if err := gw.syncNonce(); err != nil {
		return nil, err
	}

	return gw, nil
}

func (g *EthGateway) syncNonce() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	nonce, err := g.client.PendingNonceAt(ctx, g.wallet.Address)
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

func (g *EthGateway) BalanceOfNative(ctx context.Context) (*big.Int, error) {
	if g == nil || g.client == nil {
		return nil, context.Canceled
	}
	bal, err := g.client.BalanceAt(ctx, g.wallet.Address, nil)
	if err != nil {
		return nil, fmt.Errorf("balance native failed: %w", err)
	}
	return bal, nil
}

func (g *EthGateway) AllowanceERC20(ctx context.Context, token, owner, spender common.Address) (*big.Int, error) {
	data, err := erc20ABI.Pack("allowance", owner, spender)
	if err != nil {
		return nil, fmt.Errorf("pack allowance failed: %w", err)
	}
	call := ethereum.CallMsg{To: &token, Data: data}
	res, err := g.client.CallContract(ctx, call, nil)
	if err != nil {
		return nil, fmt.Errorf("call allowance failed: %w", err)
	}
	values, err := erc20ABI.Unpack("allowance", res)
	if err != nil || len(values) == 0 {
		return nil, fmt.Errorf("unpack allowance failed: %w", err)
	}
	allow, ok := values[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected allowance type %T", values[0])
	}
	return allow, nil
}

func (g *EthGateway) ApproveERC20(ctx context.Context, token, spender common.Address, amount *big.Int) (*TxResult, error) {
	if amount == nil {
		amount = big.NewInt(0)
	}
	data, err := erc20ABI.Pack("approve", spender, amount)
	if err != nil {
		return nil, fmt.Errorf("pack approve failed: %w", err)
	}

	g.nonceMu.Lock()
	defer g.nonceMu.Unlock()

	// Try estimate; fall back to a reasonable cap.
	gasLimit := uint64(120_000)
	if est, err := g.client.EstimateGas(ctx, ethereum.CallMsg{From: g.wallet.Address, To: &token, Data: data}); err == nil && est > 0 {
		gasLimit = est + 20_000
	}
	tipCap, err := g.client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("gas tip error: %w", err)
	}
	// Ensure some headroom; some chains return 0 tip for empty mempools.
	minTip := big.NewInt(1_000_000) // 0.001 gwei
	if tipCap.Cmp(minTip) < 0 {
		tipCap = minTip
	}
	header, err := g.client.HeaderByNumber(ctx, nil)
	if err != nil || header == nil || header.BaseFee == nil {
		return nil, fmt.Errorf("base fee unavailable: %w", err)
	}
	// Base fee can jump between blocks; use a multiplier to avoid "maxFeePerGas < baseFee" failures.
	feeCap := new(big.Int).Add(
		new(big.Int).Mul(header.BaseFee, big.NewInt(2)),
		new(big.Int).Mul(tipCap, big.NewInt(2)),
	)

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   g.chainID,
		Nonce:     g.nonce,
		To:        &token,
		Value:     big.NewInt(0),
		Gas:       gasLimit,
		GasTipCap: tipCap,
		GasFeeCap: feeCap,
		Data:      data,
	})
	signedTx, err := types.SignTx(tx, types.NewLondonSigner(g.chainID), g.wallet.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign approve tx: %w", err)
	}
	if err := g.client.SendTransaction(ctx, signedTx); err != nil {
		return nil, fmt.Errorf("send approve tx failed: %w", err)
	}
	log.Printf("[Gateway] Sent approve Tx Hash: %s", signedTx.Hash().Hex())
	g.nonce++

	go g.waitReceipt(signedTx.Hash())

	return &TxResult{Hash: signedTx.Hash(), Status: StatusPending}, nil
}

func (g *EthGateway) WaitMined(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		receipt, err := g.client.TransactionReceipt(ctx, hash)
		if err == nil && receipt != nil {
			return receipt, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (g *EthGateway) Send(ctx context.Context, intent contracts.Intent) (*TxResult, error) {
	g.nonceMu.Lock()
	defer g.nonceMu.Unlock()

	log.Printf("[Gateway] Processing Intent %s. Nonce: %d", intent.ID, g.nonce)

	// 1. 构造交易请求（未来由 Adapter 提供）
	var callData []byte
	if hexData := intent.Metadata["calldata"]; hexData != "" {
		if data, err := hex.DecodeString(strings.TrimPrefix(hexData, "0x")); err == nil {
			callData = data
		}
	}
	toMeta := intent.Metadata["target"]
	if toMeta == "" {
		toMeta = "0x0000000000000000000000000000000000000000"
	}
	toAddr := common.HexToAddress(toMeta)
	value := big.NewInt(0)
	if valStr := intent.Metadata["value"]; valStr != "" {
		if val, ok := new(big.Int).SetString(valStr, 10); ok {
			value = val
		}
	}

	tx := types.NewTransaction(g.nonce, toAddr, value, 800000, big.NewInt(0), callData)

	// 2. Gas 估算与价格
	gasLimit := uint64(800000)
	if est, err := g.client.EstimateGas(ctx, ethereum.CallMsg{From: g.wallet.Address, To: &toAddr, Value: value, Data: callData}); err == nil && est > 0 {
		gasLimit = est + 50_000
	}
	tipCap, err := g.client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("gas tip error: %w", err)
	}
	minTip := big.NewInt(1_000_000) // 0.001 gwei
	if tipCap.Cmp(minTip) < 0 {
		tipCap = minTip
	}
	header, err := g.client.HeaderByNumber(ctx, nil)
	if err != nil || header == nil || header.BaseFee == nil {
		return nil, fmt.Errorf("base fee unavailable: %w", err)
	}
	feeCap := new(big.Int).Add(
		new(big.Int).Mul(header.BaseFee, big.NewInt(2)),
		new(big.Int).Mul(tipCap, big.NewInt(2)),
	)

	tx = types.NewTx(&types.DynamicFeeTx{
		ChainID:   g.chainID,
		Nonce:     g.nonce,
		To:        &toAddr,
		Value:     value,
		Gas:       gasLimit,
		GasTipCap: tipCap,
		GasFeeCap: feeCap,
		Data:      callData,
	})

	// 3. 签名
	signedTx, err := types.SignTx(tx, types.NewLondonSigner(g.chainID), g.wallet.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign tx: %w", err)
	}

	if err := g.client.SendTransaction(ctx, signedTx); err != nil {
		return nil, fmt.Errorf("send tx failed: %w", err)
	}
	log.Printf("[Gateway] Sent Tx Hash: %s", signedTx.Hash().Hex())

	g.nonce++

	// 4. 等待回执（简化轮询）
	go g.waitReceipt(signedTx.Hash())

	return &TxResult{
		Hash:   signedTx.Hash(),
		Status: StatusPending,
	}, nil
}

func (g *EthGateway) waitReceipt(hash common.Hash) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

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
		gasPriceWei := receipt.EffectiveGasPrice
		if gasPriceWei == nil {
			gasPriceWei = big.NewInt(0)
		}
		select {
		case g.receiptCh <- ReceiptResult{
			Hash:        hash,
			Status:      txStatus,
			GasUsed:     receipt.GasUsed,
			GasPriceWei: gasPriceWei,
		}:
		default:
			log.Printf("[Gateway] receipt channel full, drop %s", hash.Hex())
		}
		return
	}
}
