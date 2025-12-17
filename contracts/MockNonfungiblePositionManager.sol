// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

interface IERC20Like {
    function transferFrom(address from, address to, uint256 amount) external returns (bool);
}

/**
 * @title MockNonfungiblePositionManager
 * @notice Minimal testnet-only mock that matches Phoenix's ABI usage:
 *         - mint(params) -> (tokenId, liquidity, amount0, amount1)
 *         - positions(tokenId) -> tuple
 *         - balanceOf(owner), tokenOfOwnerByIndex(owner,index)
 *         - decreaseLiquidity, collect, burn
 *
 * This does NOT implement real UniV3 logic; it exists only to validate Phoenix's
 * calldata/approval/broadcast/audit plumbing on testnet.
 */
contract MockNonfungiblePositionManager {
    struct Position {
        address operator;
        address token0;
        address token1;
        uint24 fee;
        int24 tickLower;
        int24 tickUpper;
        uint128 liquidity;
        uint256 feeGrowthInside0LastX128;
        uint256 feeGrowthInside1LastX128;
        uint128 tokensOwed0;
        uint128 tokensOwed1;
    }

    struct MintParams {
        address token0;
        address token1;
        uint24 fee;
        int24 tickLower;
        int24 tickUpper;
        uint256 amount0Desired;
        uint256 amount1Desired;
        uint256 amount0Min;
        uint256 amount1Min;
        address recipient;
        uint256 deadline;
    }

    struct DecreaseLiquidityParams {
        uint256 tokenId;
        uint128 liquidity;
        uint256 amount0Min;
        uint256 amount1Min;
        uint256 deadline;
    }

    struct CollectParams {
        uint256 tokenId;
        address recipient;
        uint128 amount0Max;
        uint128 amount1Max;
    }

    uint256 private _nextTokenId = 1;

    mapping(uint256 => Position) private _positions;
    mapping(uint256 => address) private _ownerOf;
    mapping(address => uint256[]) private _owned;

    event Minted(uint256 indexed tokenId, address indexed owner, address token0, address token1, uint24 fee, int24 tickLower, int24 tickUpper, uint256 amount0, uint256 amount1);
    event Burned(uint256 indexed tokenId, address indexed owner);

    function balanceOf(address owner) external view returns (uint256) {
        return _owned[owner].length;
    }

    function tokenOfOwnerByIndex(address owner, uint256 index) external view returns (uint256) {
        require(index < _owned[owner].length, "index out of bounds");
        return _owned[owner][index];
    }

    function positions(uint256 tokenId)
        external
        view
        returns (
            uint96 nonce,
            address operator,
            address token0,
            address token1,
            uint24 fee,
            int24 tickLower,
            int24 tickUpper,
            uint128 liquidity,
            uint256 feeGrowthInside0LastX128,
            uint256 feeGrowthInside1LastX128,
            uint128 tokensOwed0,
            uint128 tokensOwed1
        )
    {
        Position memory p = _positions[tokenId];
        return (
            uint96(0),
            p.operator,
            p.token0,
            p.token1,
            p.fee,
            p.tickLower,
            p.tickUpper,
            p.liquidity,
            p.feeGrowthInside0LastX128,
            p.feeGrowthInside1LastX128,
            p.tokensOwed0,
            p.tokensOwed1
        );
    }

    function mint(MintParams calldata params)
        external
        payable
        returns (uint256 tokenId, uint128 liquidity, uint256 amount0, uint256 amount1)
    {
        require(block.timestamp <= params.deadline, "deadline passed");
        require(params.recipient != address(0), "zero recipient");
        require(params.tickLower < params.tickUpper, "invalid ticks");

        // Take tokens (requires approve from caller).
        if (params.amount0Desired > 0) {
            require(IERC20Like(params.token0).transferFrom(msg.sender, address(this), params.amount0Desired), "t0 transferFrom failed");
        }
        if (params.amount1Desired > 0) {
            require(IERC20Like(params.token1).transferFrom(msg.sender, address(this), params.amount1Desired), "t1 transferFrom failed");
        }

        tokenId = _nextTokenId++;
        amount0 = params.amount0Desired;
        amount1 = params.amount1Desired;
        liquidity = uint128(amount0 + amount1);

        _ownerOf[tokenId] = params.recipient;
        _owned[params.recipient].push(tokenId);
        _positions[tokenId] = Position({
            operator: params.recipient,
            token0: params.token0,
            token1: params.token1,
            fee: params.fee,
            tickLower: params.tickLower,
            tickUpper: params.tickUpper,
            liquidity: liquidity,
            feeGrowthInside0LastX128: 0,
            feeGrowthInside1LastX128: 0,
            tokensOwed0: 0,
            tokensOwed1: 0
        });

        emit Minted(tokenId, params.recipient, params.token0, params.token1, params.fee, params.tickLower, params.tickUpper, amount0, amount1);
    }

    function decreaseLiquidity(DecreaseLiquidityParams calldata params) external payable returns (uint256 amount0, uint256 amount1) {
        address owner = _ownerOf[params.tokenId];
        require(owner != address(0), "unknown tokenId");
        Position storage p = _positions[params.tokenId];
        require(p.liquidity >= params.liquidity, "insufficient liq");
        p.liquidity -= params.liquidity;
        amount0 = 0;
        amount1 = 0;
    }

    function collect(CollectParams calldata params) external payable returns (uint256 amount0, uint256 amount1) {
        address owner = _ownerOf[params.tokenId];
        require(owner != address(0), "unknown tokenId");
        require(params.recipient != address(0), "zero recipient");
        amount0 = 0;
        amount1 = 0;
    }

    function burn(uint256 tokenId) external payable {
        address owner = _ownerOf[tokenId];
        require(owner != address(0), "unknown tokenId");
        _removeOwned(owner, tokenId);
        delete _ownerOf[tokenId];
        delete _positions[tokenId];
        emit Burned(tokenId, owner);
    }

    function _removeOwned(address owner, uint256 tokenId) internal {
        uint256[] storage ids = _owned[owner];
        for (uint256 i = 0; i < ids.length; i++) {
            if (ids[i] == tokenId) {
                ids[i] = ids[ids.length - 1];
                ids.pop();
                return;
            }
        }
    }
}

