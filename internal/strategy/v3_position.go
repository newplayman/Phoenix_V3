package strategy

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

type PositionSource string

const (
	PositionSourceOnchain       PositionSource = "onchain"
	PositionSourceConfigAssumed PositionSource = "config_assumed"
	PositionSourceNone          PositionSource = "none"
)

type PositionState struct {
	LowerTick int64
	UpperTick int64
	TokenID   uint64
	Source    PositionSource
	UpdatedAt time.Time
}

type V3PositionResolver struct {
	fetcher onchainPositionFetcher
}

func NewV3PositionResolver() *V3PositionResolver {
	return &V3PositionResolver{fetcher: &ethOnchainPositionFetcher{}}
}

type onchainPositionFetcher interface {
	Fetch(ctx context.Context, now time.Time, rpcURL, npmAddr string, tokenID uint64) (lower, upper int64, updatedAt time.Time, err error)
}

func (r *V3PositionResolver) Resolve(ctx context.Context, now time.Time, cfg V3RebalanceConfig) PositionState {
	// Priority 1: onchain
	if cfg.PositionTokenID > 0 && strings.TrimSpace(cfg.NPMAddress) != "" && strings.TrimSpace(cfg.ChainRPCURL) != "" {
		lower, upper, updatedAt, err := r.fetcher.Fetch(ctx, now, cfg.ChainRPCURL, cfg.NPMAddress, cfg.PositionTokenID)
		if err == nil && upper > lower {
			return PositionState{
				LowerTick: lower,
				UpperTick: upper,
				TokenID:   cfg.PositionTokenID,
				Source:    PositionSourceOnchain,
				UpdatedAt: updatedAt,
			}
		}
	}

	// Priority 2: config/env assumed (explicit)
	if cfg.HasAssumedRange {
		return PositionState{
			LowerTick: cfg.AssumedLowerTick,
			UpperTick: cfg.AssumedUpperTick,
			TokenID:   0,
			Source:    PositionSourceConfigAssumed,
			UpdatedAt: now,
		}
	}

	// Priority 3: none
	return PositionState{Source: PositionSourceNone}
}

var npmPositionsABI = mustParseABI(`[
  {
    "inputs":[{"internalType":"uint256","name":"tokenId","type":"uint256"}],
    "name":"positions",
    "outputs":[
      {"internalType":"uint96","name":"nonce","type":"uint96"},
      {"internalType":"address","name":"operator","type":"address"},
      {"internalType":"address","name":"token0","type":"address"},
      {"internalType":"address","name":"token1","type":"address"},
      {"internalType":"uint24","name":"fee","type":"uint24"},
      {"internalType":"int24","name":"tickLower","type":"int24"},
      {"internalType":"int24","name":"tickUpper","type":"int24"},
      {"internalType":"uint128","name":"liquidity","type":"uint128"},
      {"internalType":"uint256","name":"feeGrowthInside0LastX128","type":"uint256"},
      {"internalType":"uint256","name":"feeGrowthInside1LastX128","type":"uint256"},
      {"internalType":"uint128","name":"tokensOwed0","type":"uint128"},
      {"internalType":"uint128","name":"tokensOwed1","type":"uint128"}
    ],
    "stateMutability":"view",
    "type":"function"
  }
]`)

func mustParseABI(raw string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic(err)
	}
	return a
}

type ethOnchainPositionFetcher struct {
	mu sync.Mutex

	rpcURL string
	client *ethclient.Client
}

func (f *ethOnchainPositionFetcher) Fetch(ctx context.Context, now time.Time, rpcURL, npmAddr string, tokenID uint64) (lower, upper int64, updatedAt time.Time, err error) {
	addr := common.HexToAddress(strings.TrimSpace(npmAddr))
	if addr == (common.Address{}) {
		return 0, 0, time.Time{}, errors.New("invalid npm address")
	}

	f.mu.Lock()
	if f.client == nil || f.rpcURL != rpcURL {
		if f.client != nil {
			f.client.Close()
		}
		f.rpcURL = rpcURL
		f.client, err = ethclient.Dial(rpcURL)
		if err != nil {
			f.mu.Unlock()
			return 0, 0, time.Time{}, err
		}
	}
	client := f.client
	f.mu.Unlock()

	data, err := npmPositionsABI.Pack("positions", new(big.Int).SetUint64(tokenID))
	if err != nil {
		return 0, 0, time.Time{}, err
	}

	callCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()

	out, err := client.CallContract(callCtx, ethereum.CallMsg{To: &addr, Data: data}, nil)
	if err != nil {
		return 0, 0, time.Time{}, err
	}

	decoded, err := npmPositionsABI.Unpack("positions", out)
	if err != nil || len(decoded) < 7 {
		return 0, 0, time.Time{}, errors.New("decode positions failed")
	}

	// outputs[5]=tickLower (int24), outputs[6]=tickUpper (int24)
	lower, ok1 := toInt64(decoded[5])
	upper, ok2 := toInt64(decoded[6])
	if !ok1 || !ok2 {
		return 0, 0, time.Time{}, errors.New("unexpected tick types")
	}
	return lower, upper, now, nil
}

func toInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int8:
		return int64(t), true
	case int16:
		return int64(t), true
	case int32:
		return int64(t), true
	case int64:
		return t, true
	case *big.Int:
		return t.Int64(), true
	default:
		return 0, false
	}
}
