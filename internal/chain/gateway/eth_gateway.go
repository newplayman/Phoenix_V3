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
	Hash    common.Hash
	Status  TxStatus
	GasUsed uint64
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
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to dial rpc: %w", err)
	}

	wallet, err := chain.NewWallet(privKey)
	if err != nil {
		return nil, err
	}

	chainID, err := client.ChainID(context.Background())
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
	gasPrice, err := g.client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("gas price error: %w", err)
	}
	tx = types.NewTransaction(g.nonce, toAddr, value, 800000, gasPrice, callData)

	// 3. 签名
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(g.chainID), g.wallet.PrivateKey)
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
		select {
		case g.receiptCh <- ReceiptResult{
			Hash:    hash,
			Status:  txStatus,
			GasUsed: receipt.GasUsed,
		}:
		default:
			log.Printf("[Gateway] receipt channel full, drop %s", hash.Hex())
		}
		return
	}
}
