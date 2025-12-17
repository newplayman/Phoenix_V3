package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"math/big"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"phoenix-v3/internal/chain/univ3"
)

type senderCount struct {
	Addr  string
	Count int
}

type mintEntry struct {
	TxHash      common.Hash
	BlockNumber uint64
	LogIndex    uint
	Sender      common.Address
	Owner       common.Address
	TickLower   int32
	TickUpper   int32
}

type poolMeta struct {
	Token0 common.Address
	Token1 common.Address
	Fee    uint32
	OK     bool
}

func addrFromTopic(h common.Hash) common.Address {
	return common.BytesToAddress(h.Bytes()[12:32])
}

func int24FromTopic(h common.Hash) (int32, error) {
	i := new(big.Int).SetBytes(h.Bytes())
	// Interpret as signed 256-bit integer.
	if i.Bit(255) == 1 {
		i.Sub(i, new(big.Int).Lsh(big.NewInt(1), 256))
	}
	v := i.Int64()
	if v < math.MinInt32 || v > math.MaxInt32 {
		return 0, fmt.Errorf("topic int out of int32 range: %d", v)
	}
	if v < -8388608 || v > 8388607 {
		return 0, fmt.Errorf("topic int out of int24 range: %d", v)
	}
	return int32(v), nil
}

func callPoolMeta(ctx context.Context, client *ethclient.Client, pool common.Address) (poolMeta, error) {
	parsed, err := abi.JSON(strings.NewReader(`[
		{"inputs":[],"name":"token0","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},
		{"inputs":[],"name":"token1","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"},
		{"inputs":[],"name":"fee","outputs":[{"internalType":"uint24","name":"","type":"uint24"}],"stateMutability":"view","type":"function"}
	]`))
	if err != nil {
		return poolMeta{}, fmt.Errorf("abi parse: %w", err)
	}
	callView := func(method string) ([]byte, error) {
		data, err := parsed.Pack(method)
		if err != nil {
			return nil, err
		}
		msg := ethereum.CallMsg{To: &pool, Data: data}
		return client.CallContract(ctx, msg, nil)
	}
	readAddr := func(method string) (common.Address, error) {
		b, err := callView(method)
		if err != nil {
			return common.Address{}, err
		}
		vals, err := parsed.Unpack(method, b)
		if err != nil || len(vals) < 1 {
			return common.Address{}, fmt.Errorf("unpack %s failed: %w", method, err)
		}
		a, ok := vals[0].(common.Address)
		if !ok {
			return common.Address{}, fmt.Errorf("unexpected %s type: %T", method, vals[0])
		}
		return a, nil
	}
	readFee := func() (uint32, error) {
		b, err := callView("fee")
		if err != nil {
			return 0, err
		}
		vals, err := parsed.Unpack("fee", b)
		if err != nil || len(vals) < 1 {
			return 0, fmt.Errorf("unpack fee failed: %w", err)
		}
		switch v := vals[0].(type) {
		case *big.Int:
			return uint32(v.Uint64()), nil
		case uint32:
			return v, nil
		case uint64:
			return uint32(v), nil
		default:
			return 0, fmt.Errorf("unexpected fee type: %T", vals[0])
		}
	}

	t0, err := readAddr("token0")
	if err != nil {
		return poolMeta{}, err
	}
	t1, err := readAddr("token1")
	if err != nil {
		return poolMeta{}, err
	}
	fee, err := readFee()
	if err != nil {
		return poolMeta{}, err
	}
	return poolMeta{Token0: t0, Token1: t1, Fee: fee, OK: t0 != (common.Address{}) && t1 != (common.Address{})}, nil
}

func main() {
	var (
		rpcURL     = flag.String("rpc", os.Getenv("ARBITRUM_SEPOLIA_RPC_URL"), "RPC URL (ARBITRUM_SEPOLIA_RPC_URL)")
		chainID    = flag.Int64("chain-id", 421614, "chain id (must be 421614 for Arbitrum Sepolia)")
		poolStr    = flag.String("pool", "", "pool address (0x...)")
		lookback   = flag.Uint64("lookback", 200000, "how many latest blocks to scan for UniV3 Mint events on this pool")
		timeout    = flag.Duration("timeout", 60*time.Second, "RPC timeout")
		maxResults = flag.Int("max-results", 5000, "max log results before aborting (safety)")
		details    = flag.Bool("details", false, "print per-mint details (tx hash, owner, ticks)")
		trace      = flag.Bool("trace", false, "fetch receipts for mints and try to derive position manager + tokenId via ERC721 Transfer + positions()")
		maxTrace   = flag.Int("max-trace", 25, "max number of mint receipts to trace (safety)")
	)
	flag.Parse()

	if strings.TrimSpace(*rpcURL) == "" {
		log.Fatal("missing rpc url (set -rpc or ARBITRUM_SEPOLIA_RPC_URL)")
	}
	if *chainID != 421614 {
		log.Fatalf("blocked: chain id %d (only Arbitrum Sepolia 421614 allowed)", *chainID)
	}
	if !common.IsHexAddress(*poolStr) {
		log.Fatalf("missing/invalid -pool address: %q", *poolStr)
	}
	pool := common.HexToAddress(*poolStr)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client, err := ethclient.DialContext(ctx, *rpcURL)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	gotChainID, err := client.ChainID(ctx)
	if err != nil {
		log.Fatalf("chain id: %v", err)
	}
	if gotChainID.Cmp(big.NewInt(*chainID)) != 0 {
		log.Fatalf("unexpected chain id: got %s want %d", gotChainID.String(), *chainID)
	}

	latest, err := client.BlockNumber(ctx)
	if err != nil {
		log.Fatalf("blockNumber: %v", err)
	}
	var from uint64
	if latest > *lookback {
		from = latest - *lookback
	}

	// UniswapV3Pool event:
	// Mint(address sender, address indexed owner, int24 indexed tickLower, int24 indexed tickUpper, uint128 amount, uint256 amount0, uint256 amount1)
	mintSig := crypto.Keccak256Hash([]byte("Mint(address,address,int24,int24,uint128,uint256,uint256)"))
	q := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(from),
		ToBlock:   new(big.Int).SetUint64(latest),
		Addresses: []common.Address{pool},
		Topics:    [][]common.Hash{{mintSig}},
	}
	logs, err := client.FilterLogs(ctx, q)
	if err != nil {
		log.Fatalf("filterLogs failed (pool=%s from=%d to=%d): %v", pool.Hex(), from, latest, err)
	}
	if *maxResults > 0 && len(logs) > *maxResults {
		log.Fatalf("too many logs (%d) for safety; reduce -lookback or increase -max-results", len(logs))
	}

	counts := map[string]int{}
	mints := make([]mintEntry, 0, len(logs))
	for _, lg := range logs {
		// sender is the first non-indexed param; ABI encodes it as 32 bytes (address right-aligned).
		if len(lg.Data) < 32 {
			continue
		}
		senderAddr := common.BytesToAddress(lg.Data[12:32])
		sender := senderAddr.Hex()
		counts[strings.ToLower(sender)]++

		owner := common.Address{}
		var tickLower, tickUpper int32
		if len(lg.Topics) >= 4 {
			owner = addrFromTopic(lg.Topics[1])
			tl, err := int24FromTopic(lg.Topics[2])
			if err == nil {
				tickLower = tl
			}
			tu, err := int24FromTopic(lg.Topics[3])
			if err == nil {
				tickUpper = tu
			}
		}
		mints = append(mints, mintEntry{
			TxHash:      lg.TxHash,
			BlockNumber: lg.BlockNumber,
			LogIndex:    lg.Index,
			Sender:      senderAddr,
			Owner:       owner,
			TickLower:   tickLower,
			TickUpper:   tickUpper,
		})
	}
	out := make([]senderCount, 0, len(counts))
	for a, c := range counts {
		out = append(out, senderCount{Addr: a, Count: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Addr < out[j].Addr
	})

	fmt.Printf("chain_id=%d pool=%s from_block=%d to_block=%d mint_logs=%d unique_senders=%d\n", *chainID, pool.Hex(), from, latest, len(logs), len(out))
	for _, sc := range out {
		fmt.Printf("%s count=%d\n", sc.Addr, sc.Count)
	}

	if *details {
		sort.Slice(mints, func(i, j int) bool {
			if mints[i].BlockNumber != mints[j].BlockNumber {
				return mints[i].BlockNumber < mints[j].BlockNumber
			}
			return mints[i].LogIndex < mints[j].LogIndex
		})
		fmt.Println("---- mint_details ----")
		for _, m := range mints {
			fmt.Printf("block=%d tx=%s sender=%s owner=%s ticks=[%d,%d]\n",
				m.BlockNumber, m.TxHash.Hex(), m.Sender.Hex(), m.Owner.Hex(), m.TickLower, m.TickUpper)
		}
	}

	if *trace {
		sort.Slice(mints, func(i, j int) bool {
			if mints[i].BlockNumber != mints[j].BlockNumber {
				return mints[i].BlockNumber < mints[j].BlockNumber
			}
			return mints[i].LogIndex < mints[j].LogIndex
		})
		if *maxTrace <= 0 {
			log.Fatal("invalid -max-trace (must be >0)")
		}
		start := 0
		if len(mints) > *maxTrace {
			start = len(mints) - *maxTrace
		}
		toTrace := mints[start:]

		meta, metaErr := callPoolMeta(ctx, client, pool)
		if metaErr != nil {
			fmt.Printf("---- trace ----\n")
			fmt.Printf("pool_meta=unavailable err=%v\n", metaErr)
		} else {
			fmt.Printf("---- trace ----\n")
			fmt.Printf("pool_meta token0=%s token1=%s fee=%d\n", meta.Token0.Hex(), meta.Token1.Hex(), meta.Fee)
		}

		transferSig := crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
		pmCounts := map[string]int{}
		matched := 0

		for _, m := range toTrace {
			rcpt, err := client.TransactionReceipt(ctx, m.TxHash)
			if err != nil {
				fmt.Printf("tx=%s receipt_err=%v\n", m.TxHash.Hex(), err)
				continue
			}

			type cand struct {
				Contract common.Address
				From     common.Address
				To       common.Address
				TokenID  *big.Int
			}

			cands := make([]cand, 0, 4)
			preferred := make([]cand, 0, 2)
			for _, lg := range rcpt.Logs {
				if len(lg.Topics) != 4 || lg.Topics[0] != transferSig {
					continue
				}
				fromAddr := addrFromTopic(lg.Topics[1])
				if fromAddr != (common.Address{}) {
					continue // not a mint (require from==0x0)
				}
				toAddr := addrFromTopic(lg.Topics[2])
				tokenID := new(big.Int).SetBytes(lg.Topics[3].Bytes())
				c := cand{Contract: lg.Address, From: fromAddr, To: toAddr, TokenID: tokenID}
				cands = append(cands, c)
				if lg.Address == m.Sender {
					preferred = append(preferred, c)
				}
			}
			chosen := cands
			if len(preferred) > 0 {
				chosen = preferred
			}

			if len(chosen) == 0 {
				fmt.Printf("block=%d tx=%s sender=%s ticks=[%d,%d] nft_mints=0\n",
					m.BlockNumber, m.TxHash.Hex(), m.Sender.Hex(), m.TickLower, m.TickUpper)
				continue
			}

			// Try to identify which ERC721 mint corresponds to this pool mint by calling positions(tokenId).
			foundForTx := 0
			for _, c := range chosen {
				pmAddr := c.Contract
				adapter := univ3.NewAdapter(pmAddr.Hex())
				data, err := adapter.ParsedABI.Pack("positions", c.TokenID)
				if err != nil {
					continue
				}
				msg := ethereum.CallMsg{To: &pmAddr, Data: data}
				res, err := client.CallContract(ctx, msg, nil)
				if err != nil {
					continue
				}
				decoded, err := adapter.ParsedABI.Unpack("positions", res)
				if err != nil || len(decoded) < 7 {
					continue
				}

				var posToken0, posToken1 common.Address
				var posFee uint32
				var posTickLower, posTickUpper int64

				if v, ok := decoded[2].(common.Address); ok {
					posToken0 = v
				}
				if v, ok := decoded[3].(common.Address); ok {
					posToken1 = v
				}
				switch v := decoded[4].(type) {
				case *big.Int:
					posFee = uint32(v.Uint64())
				case uint32:
					posFee = v
				case uint64:
					posFee = uint32(v)
				}
				switch v := decoded[5].(type) {
				case *big.Int:
					posTickLower = v.Int64()
				case int64:
					posTickLower = v
				case int32:
					posTickLower = int64(v)
				}
				switch v := decoded[6].(type) {
				case *big.Int:
					posTickUpper = v.Int64()
				case int64:
					posTickUpper = v
				case int32:
					posTickUpper = int64(v)
				}

				if meta.OK {
					if posToken0 != meta.Token0 || posToken1 != meta.Token1 || posFee != meta.Fee {
						continue
					}
				}
				if m.TickLower != 0 || m.TickUpper != 0 {
					if posTickLower != int64(m.TickLower) || posTickUpper != int64(m.TickUpper) {
						continue
					}
				}

				pmCounts[strings.ToLower(pmAddr.Hex())]++
				matched++
				foundForTx++
				fmt.Printf("block=%d tx=%s sender=%s pool_owner=%s ticks=[%d,%d] pm=%s token_id=%s nft_to=%s\n",
					m.BlockNumber, m.TxHash.Hex(), m.Sender.Hex(), m.Owner.Hex(), m.TickLower, m.TickUpper, pmAddr.Hex(), c.TokenID.String(), c.To.Hex())
			}

			if foundForTx == 0 {
				fmt.Printf("block=%d tx=%s sender=%s ticks=[%d,%d] nft_mints=%d pm_match=0\n",
					m.BlockNumber, m.TxHash.Hex(), m.Sender.Hex(), m.TickLower, m.TickUpper, len(chosen))
			}
		}

		if len(pmCounts) > 0 {
			pmList := make([]senderCount, 0, len(pmCounts))
			for a, c := range pmCounts {
				pmList = append(pmList, senderCount{Addr: a, Count: c})
			}
			sort.Slice(pmList, func(i, j int) bool {
				if pmList[i].Count != pmList[j].Count {
					return pmList[i].Count > pmList[j].Count
				}
				return pmList[i].Addr < pmList[j].Addr
			})
			fmt.Printf("---- trace_summary ----\n")
			fmt.Printf("traced=%d matched=%d unique_position_managers=%d\n", len(toTrace), matched, len(pmList))
			for _, sc := range pmList {
				fmt.Printf("%s matches=%d\n", sc.Addr, sc.Count)
			}
		} else {
			fmt.Printf("---- trace_summary ----\n")
			fmt.Printf("traced=%d matched=0 unique_position_managers=0\n", len(toTrace))
		}
	}
}
