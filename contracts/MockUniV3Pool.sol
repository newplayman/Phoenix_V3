pragma solidity ^0.8.20;

contract MockUniV3Pool {
    struct Slot0Data {
        uint160 sqrtPriceX96;
        int24 tick;
        uint16 observationIndex;
        uint16 observationCardinality;
        uint16 observationCardinalityNext;
        uint8 feeProtocol;
        bool unlocked;
    }

    Slot0Data private _slot0;
    uint128 private _liquidity;

    constructor(uint160 sqrtPriceX96_, int24 tick_, uint128 liquidity_) {
        _slot0 = Slot0Data({
            sqrtPriceX96: sqrtPriceX96_,
            tick: tick_,
            observationIndex: 0,
            observationCardinality: 1,
            observationCardinalityNext: 1,
            feeProtocol: 0,
            unlocked: true
        });
        _liquidity = liquidity_;
    }

    function slot0()
        external
        view
        returns (
            uint160 sqrtPriceX96,
            int24 tick,
            uint16 observationIndex,
            uint16 observationCardinality,
            uint16 observationCardinalityNext,
            uint8 feeProtocol,
            bool unlocked
        )
    {
        Slot0Data memory s = _slot0;
        return (
            s.sqrtPriceX96,
            s.tick,
            s.observationIndex,
            s.observationCardinality,
            s.observationCardinalityNext,
            s.feeProtocol,
            s.unlocked
        );
    }

    function liquidity() external view returns (uint128) {
        return _liquidity;
    }
}

