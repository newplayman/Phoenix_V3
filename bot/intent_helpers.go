package bot

import (
	"context"
	"math/big"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

type ERC20BalanceReader interface {
	BalanceOfERC20(ctx context.Context, token common.Address) (*big.Int, error)
}

func ParseMetadataFloat(meta map[string]string, key string) float64 {
	if meta == nil {
		return 0
	}
	val, ok := meta[key]
	if !ok {
		return 0
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0
	}
	return f
}

func HasSufficientBalances(ctx context.Context, gw ERC20BalanceReader, meta map[string]string) bool {
	if gw == nil || meta == nil {
		return false
	}
	token0 := meta["token0"]
	amount0 := meta["amount0"]
	if token0 != "" && amount0 != "" {
		required := new(big.Int)
		if _, ok := required.SetString(amount0, 10); ok {
			bal, err := gw.BalanceOfERC20(ctx, common.HexToAddress(token0))
			if err != nil || bal.Cmp(required) < 0 {
				return false
			}
		}
	}
	token1 := meta["token1"]
	amount1 := meta["amount1"]
	if token1 != "" && amount1 != "" {
		required := new(big.Int)
		if _, ok := required.SetString(amount1, 10); ok {
			bal, err := gw.BalanceOfERC20(ctx, common.HexToAddress(token1))
			if err != nil || bal.Cmp(required) < 0 {
				return false
			}
		}
	}
	return true
}

// ParseMintedPositionTokenID extracts the UniV3 position NFT tokenId from a PositionManager receipt.
// It looks for Transfer(from=0x0, to=recipient, tokenId) emitted by the PositionManager contract.
func ParseMintedPositionTokenID(rcpt *types.Receipt, positionManager common.Address, recipient common.Address) *big.Int {
	if rcpt == nil {
		return nil
	}
	transferSig := crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	for _, lg := range rcpt.Logs {
		if lg == nil {
			continue
		}
		if lg.Address != positionManager {
			continue
		}
		if len(lg.Topics) < 4 {
			continue
		}
		if lg.Topics[0] != transferSig {
			continue
		}
		fromAddr := common.BytesToAddress(lg.Topics[1].Bytes()[12:])
		toAddr := common.BytesToAddress(lg.Topics[2].Bytes()[12:])
		if fromAddr != (common.Address{}) {
			continue
		}
		if toAddr != recipient {
			continue
		}
		return new(big.Int).SetBytes(lg.Topics[3].Bytes())
	}
	return nil
}
