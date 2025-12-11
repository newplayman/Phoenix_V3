// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

interface IERC20 {
    function transfer(address to, uint256 value) external returns (bool);
    function transferFrom(address from, address to, uint256 value) external returns (bool);
}

interface IUniswapV3Pool {
    function token0() external view returns (address);
    function token1() external view returns (address);
    function swap(
        address recipient,
        bool zeroForOne,
        int256 amountSpecified,
        uint160 sqrtPriceLimitX96,
        bytes calldata data
    ) external returns (int256 amount0, int256 amount1);
}

interface IUniswapV3SwapCallback {
    function uniswapV3SwapCallback(
        int256 amount0Delta,
        int256 amount1Delta,
        bytes calldata data
    ) external;
}

/**
 * @title SwapHelper
 * @notice Minimal helper that直接调用 Uniswap V3 Pool 的 swap 接口，
 *         避免依赖测试网缺失的 Router。使用前需先让用户对本合约 approve tokenIn。
 */
contract SwapHelper is IUniswapV3SwapCallback {
    uint160 private constant MIN_SQRT_RATIO = 4295128739;
    uint160 private constant MAX_SQRT_RATIO =
        1461446703485210103287273052203988822378723970342;

    struct SwapCallbackData {
        address tokenIn;
        address tokenOut;
        address pool;
    }

    error InvalidAmount();
    error TransferFailed();
    error TokenMismatch();
    error InvalidCaller();

    /**
     * @notice 用固定输入数量执行一次单池 swap
     * @param pool  Uniswap V3 池地址（需与 tokenIn/tokenOut 匹配）
     * @param tokenIn  输入资产
     * @param tokenOut 输出资产
     * @param amountIn 输入数量（整型）
     * @param sqrtPriceLimitX96 价格限制；传 0 时自动使用全区间
     */
    function swapExactInputSingle(
        address pool,
        address tokenIn,
        address tokenOut,
        uint256 amountIn,
        uint160 sqrtPriceLimitX96
    ) external returns (uint256 amountOut) {
        if (amountIn == 0) revert InvalidAmount();

        if (!IERC20(tokenIn).transferFrom(msg.sender, address(this), amountIn)) {
            revert TransferFailed();
        }

        address token0 = IUniswapV3Pool(pool).token0();
        address token1 = IUniswapV3Pool(pool).token1();

        bool zeroForOne;
        if (tokenIn == token0 && tokenOut == token1) {
            zeroForOne = true;
        } else if (tokenIn == token1 && tokenOut == token0) {
            zeroForOne = false;
        } else {
            revert TokenMismatch();
        }

        bytes memory data = abi.encode(
            SwapCallbackData({
                tokenIn: tokenIn,
                tokenOut: tokenOut,
                pool: pool
            })
        );

        uint160 priceLimit = sqrtPriceLimitX96;
        if (priceLimit == 0) {
            priceLimit = zeroForOne
                ? MIN_SQRT_RATIO + 1
                : MAX_SQRT_RATIO - 1;
        }

        (int256 amount0Delta, int256 amount1Delta) = IUniswapV3Pool(pool).swap(
            address(this),
            zeroForOne,
            int256(amountIn),
            priceLimit,
            data
        );

        int256 delta = zeroForOne ? amount1Delta : amount0Delta;
        amountOut = uint256(delta < 0 ? -delta : delta);

        if (!IERC20(tokenOut).transfer(msg.sender, amountOut)) {
            revert TransferFailed();
        }
    }

    function uniswapV3SwapCallback(
        int256 amount0Delta,
        int256 amount1Delta,
        bytes calldata data
    ) external override {
        SwapCallbackData memory decoded = abi.decode(data, (SwapCallbackData));
        if (msg.sender != decoded.pool) revert InvalidCaller();

        uint256 amountToPay = amount0Delta > 0
            ? uint256(amount0Delta)
            : uint256(amount1Delta);

        if (!IERC20(decoded.tokenIn).transfer(decoded.pool, amountToPay)) {
            revert TransferFailed();
        }
    }
}
