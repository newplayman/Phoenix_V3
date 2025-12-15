package univ3

// Minimal ABIs for Uniswap V3 interaction

const NonfungiblePositionManagerABI = `[
	{
		"inputs": [
			{
				"components": [
					{"internalType": "address","name": "token0","type": "address"},
					{"internalType": "address","name": "token1","type": "address"},
					{"internalType": "uint24","name": "fee","type": "uint24"},
					{"internalType": "int24","name": "tickLower","type": "int24"},
					{"internalType": "int24","name": "tickUpper","type": "int24"},
					{"internalType": "uint256","name": "amount0Desired","type": "uint256"},
					{"internalType": "uint256","name": "amount1Desired","type": "uint256"},
					{"internalType": "uint256","name": "amount0Min","type": "uint256"},
					{"internalType": "uint256","name": "amount1Min","type": "uint256"},
					{"internalType": "address","name": "recipient","type": "address"},
					{"internalType": "uint256","name": "deadline","type": "uint256"}
				],
				"internalType": "struct INonfungiblePositionManager.MintParams",
				"name": "params",
				"type": "tuple"
			}
		],
		"name": "mint",
		"outputs": [
			{"internalType": "uint256","name": "tokenId","type": "uint256"},
			{"internalType": "uint128","name": "liquidity","type": "uint128"},
			{"internalType": "uint256","name": "amount0","type": "uint256"},
			{"internalType": "uint256","name": "amount1","type": "uint256"}
		],
		"stateMutability": "payable",
		"type": "function"
	},
	{
		"inputs": [{"internalType": "address","name": "owner","type": "address"}, {"internalType": "uint256","name": "index","type": "uint256"}],
		"name": "tokenOfOwnerByIndex",
		"outputs": [{"internalType": "uint256","name": "","type": "uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [{"internalType": "address","name": "owner","type": "address"}],
		"name": "balanceOf",
		"outputs": [{"internalType": "uint256","name": "","type": "uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [{"internalType": "uint256","name": "tokenId","type": "uint256"}],
		"name": "positions",
		"outputs": [
			{"internalType": "uint96","name": "nonce","type": "uint96"},
			{"internalType": "address","name": "operator","type": "address"},
			{"internalType": "address","name": "token0","type": "address"},
			{"internalType": "address","name": "token1","type": "address"},
			{"internalType": "uint24","name": "fee","type": "uint24"},
			{"internalType": "int24","name": "tickLower","type": "int24"},
			{"internalType": "int24","name": "tickUpper","type": "int24"},
			{"internalType": "uint128","name": "liquidity","type": "uint128"},
			{"internalType": "uint256","name": "feeGrowthInside0LastX128","type": "uint256"},
			{"internalType": "uint256","name": "feeGrowthInside1LastX128","type": "uint256"},
			{"internalType": "uint128","name": "tokensOwed0","type": "uint128"},
			{"internalType": "uint128","name": "tokensOwed1","type": "uint128"}
		],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [
			{
				"components": [
					{"internalType": "uint256","name": "tokenId","type": "uint256"},
					{"internalType": "uint128","name": "liquidity","type": "uint128"},
					{"internalType": "uint256","name": "amount0Min","type": "uint256"},
					{"internalType": "uint256","name": "amount1Min","type": "uint256"},
					{"internalType": "uint256","name": "deadline","type": "uint256"}
				],
				"internalType": "struct INonfungiblePositionManager.DecreaseLiquidityParams",
				"name": "params",
				"type": "tuple"
			}
		],
		"name": "decreaseLiquidity",
		"outputs": [
			{"internalType": "uint256","name": "amount0","type": "uint256"},
			{"internalType": "uint256","name": "amount1","type": "uint256"}
		],
		"stateMutability": "payable",
		"type": "function"
	},
	{
		"inputs": [
			{
				"components": [
					{"internalType": "uint256","name": "tokenId","type": "uint256"},
					{"internalType": "address","name": "recipient","type": "address"},
					{"internalType": "uint128","name": "amount0Max","type": "uint128"},
					{"internalType": "uint128","name": "amount1Max","type": "uint128"}
				],
				"internalType": "struct INonfungiblePositionManager.CollectParams",
				"name": "params",
				"type": "tuple"
			}
		],
		"name": "collect",
		"outputs": [
			{"internalType": "uint256","name": "amount0","type": "uint256"},
			{"internalType": "uint256","name": "amount1","type": "uint256"}
		],
		"stateMutability": "payable",
		"type": "function"
	},
	{
		"inputs": [{"internalType": "uint256","name": "tokenId","type": "uint256"}],
		"name": "burn",
		"outputs": [],
		"stateMutability": "payable",
		"type": "function"
	}
]`

// ISwapRouter ABI supporting exactInputSingle and exactInput (multi-hop).
const SwapRouterABI = `[
	{
		"inputs": [
			{
				"components": [
					{"internalType": "address","name": "tokenIn","type": "address"},
					{"internalType": "address","name": "tokenOut","type": "address"},
					{"internalType": "uint24","name": "fee","type": "uint24"},
					{"internalType": "address","name": "recipient","type": "address"},
					{"internalType": "uint256","name": "deadline","type": "uint256"},
					{"internalType": "uint256","name": "amountIn","type": "uint256"},
					{"internalType": "uint256","name": "amountOutMinimum","type": "uint256"},
					{"internalType": "uint160","name": "sqrtPriceLimitX96","type": "uint160"}
				],
				"internalType": "struct ISwapRouter.ExactInputSingleParams",
				"name": "params",
				"type": "tuple"
			}
		],
		"name": "exactInputSingle",
		"outputs": [
			{"internalType": "uint256","name": "amountOut","type": "uint256"}
		],
		"stateMutability": "payable",
		"type": "function"
	},
	{
		"inputs": [
			{
				"components": [
					{"internalType": "bytes","name": "path","type": "bytes"},
					{"internalType": "address","name": "recipient","type": "address"},
					{"internalType": "uint256","name": "deadline","type": "uint256"},
					{"internalType": "uint256","name": "amountIn","type": "uint256"},
					{"internalType": "uint256","name": "amountOutMinimum","type": "uint256"}
				],
				"internalType": "struct ISwapRouter.ExactInputParams",
				"name": "params",
				"type": "tuple"
			}
		],
		"name": "exactInput",
		"outputs": [
			{"internalType": "uint256","name": "amountOut","type": "uint256"}
		],
		"stateMutability": "payable",
		"type": "function"
	}
]`
