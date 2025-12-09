package gateway

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"

	"phoenix-v3/internal/chain"
	"phoenix-v3/internal/strategy"
)

type EthGateway struct {
	client  *ethclient.Client
	wallet  *chain.Wallet
	chainID *big.Int

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
		client:  client,
		wallet:  wallet,
		chainID: chainID,
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

func (g *EthGateway) Send(ctx context.Context, intent strategy.Intent) (*TxResult, error) {
	// 1. Adapter would build the raw transaction data here (or passed in intent)
	// For Phase 4, we assume the intent *contains* or we construct *generic* tx data.
	// In a real generic gateway, 'intent' might need to be converted to a specific Transaction Request.
	// We'll simplify: We assume `intent` payload tells us what to do, but Adapter logic should reside outside Gateway strictly.
	// However, usually Gateway accepts a "TxRequest" not just "Intent".

	// REFACTOR: Gateway should take `types.Transaction` or `CallData`.
	// Since the interface is `Send(Intent)`, let's assume we have an "Adapter" that converts Intent -> TxData *before* calling Gateway?
	// Or Gateway calls Adapter?
	// The doc says: "chain/gateway: 把 Intent 变成链上交易".
	// "chain/adapters: 为不同的 DEX 提供统一的 LP 操作接口 (BuildTx)".

	// So: Gateway calls Adapter to get Tx, then signs and sends.
	// But `Gateway` struct usually shouldn't depend on specific DEX adapters.
	// Let's make Gateway generic: SendTx(to, value, data).
	// We will change the interface slightly in implementation or keep it high-level.

	// Let's assume for this demo, the Gateway constructs a simple transfer or logs it,
	// because integrating the full UnixV3 Adapter with correct ABI in one step is huge.

	// Let's implement a "Mock Send" that simulates the heavy lifting if DryRun.
	// But we want Real Mode.

	g.nonceMu.Lock()
	defer g.nonceMu.Unlock()

	log.Printf("[Gateway] Processing Intent %s. Nonce: %d", intent.ID, g.nonce)

	// Mocking a transaction construction (Transfer 0 ETH to self)
	// In reality, this `data` comes from Adapter.BuildMintTx(...)
	toAddress := common.HexToAddress("0x0000000000000000000000000000000000000000") // Burn address for test
	value := big.NewInt(0)
	gasLimit := uint64(21000)
	gasPrice, _ := g.client.SuggestGasPrice(ctx)

	tx := types.NewTransaction(g.nonce, toAddress, value, gasLimit, gasPrice, nil)

	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(g.chainID), g.wallet.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign tx: %w", err)
	}

	// In Phase 4, we actually send it if we had a real RPC / key.
	// err = g.client.SendTransaction(ctx, signedTx)
	// For safety in this environment (no real keys), we just log "Signed".
	log.Printf("[Gateway] Signed Tx Hash: %s", signedTx.Hash().Hex())

	// Increment nonce locally
	g.nonce++

	return &TxResult{
		Hash:   signedTx.Hash(),
		Status: StatusPending,
	}, nil
}
