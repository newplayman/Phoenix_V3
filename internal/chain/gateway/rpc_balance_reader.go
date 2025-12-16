package gateway

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// RPCBalanceReader provides ERC20 balance reads for a fixed wallet address,
// without needing a private key (read-only).
type RPCBalanceReader struct {
	client *ethclient.Client
	wallet common.Address
}

func NewRPCBalanceReader(rpcURL string, wallet common.Address) (*RPCBalanceReader, error) {
	if wallet == (common.Address{}) {
		return nil, fmt.Errorf("rpc balance reader: zero wallet address")
	}
	httpClient := &http.Client{Timeout: 15 * time.Second}
	rpcClient, err := rpc.DialHTTPWithClient(rpcURL, httpClient)
	if err != nil {
		return nil, fmt.Errorf("rpc balance reader: dial rpc: %w", err)
	}
	return &RPCBalanceReader{
		client: ethclient.NewClient(rpcClient),
		wallet: wallet,
	}, nil
}

func (r *RPCBalanceReader) WalletAddress() common.Address { return r.wallet }

func (r *RPCBalanceReader) BalanceOfERC20(ctx context.Context, token common.Address) (*big.Int, error) {
	if r == nil || r.client == nil {
		return nil, fmt.Errorf("rpc balance reader: not initialized")
	}
	data, err := erc20ABI.Pack("balanceOf", r.wallet)
	if err != nil {
		return nil, fmt.Errorf("rpc balance reader: pack balanceOf: %w", err)
	}
	call := ethereum.CallMsg{To: &token, Data: data}
	res, err := r.client.CallContract(ctx, call, nil)
	if err != nil {
		return nil, fmt.Errorf("rpc balance reader: call balanceOf: %w", err)
	}
	values, err := erc20ABI.Unpack("balanceOf", res)
	if err != nil || len(values) == 0 {
		return nil, fmt.Errorf("rpc balance reader: unpack balanceOf: %w", err)
	}
	bal, ok := values[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("rpc balance reader: unexpected balance type %T", values[0])
	}
	return bal, nil
}
