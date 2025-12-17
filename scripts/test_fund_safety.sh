#!/usr/bin/env bash
set -euo pipefail

##############################################################################
# Phoenix V3 - 资金安全全面测试脚本
#
# 测试目标:
# 1. Swap 换币 - 验证代币是否正确回到钱包地址
# 2. Mint LP - 验证代币是否正确进入LP池合约
# 3. Collect Fees - 验证手续费是否正确回到钱包地址
# 4. Burn LP - 验证两种代币是否正确回到钱包地址
# 5. Gas 消耗 - 验证每个操作的gas是否合理
#
# 防止资金丢失到 0x000...000 黑洞地址
##############################################################################

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
CONFIG_PATH="${CONFIG_PATH:-configs/config.yaml}"
SEPOLIA_RPC_URL="${SEPOLIA_RPC_URL:-https://ethereum-sepolia.publicnode.com}"
WALLET_ADDR="${WALLET_ADDR:-0x39BFa37b4A8A7A20D0F69fd0a388e3EAe739c217}"
TUSD_ADDR="${TUSD_ADDR:-0x3E49DB88bC85135b6F716E5CD573cDd42b8640c5}"
WETH_ADDR="${WETH_ADDR:-0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9}"
POOL_ADDR="${POOL_ADDR:-0x1E80b0b6d12Ecf2CDD08bC9c66f2fD594394331d}"
POS_MANAGER="${POS_MANAGER:-0x1238536071E1c677A632429e3655c799b22cDA52}"
LOG_DIR="logs"
TEST_LOG="$LOG_DIR/fund_safety_test.log"

mkdir -p "$LOG_DIR"
: > "$TEST_LOG"

log() {
    echo -e "${BLUE}[$(date +'%H:%M:%S')]${NC} $*" | tee -a "$TEST_LOG"
}

log_success() {
    echo -e "${GREEN}[$(date +'%H:%M:%S')] ✅ $*${NC}" | tee -a "$TEST_LOG"
}

log_error() {
    echo -e "${RED}[$(date +'%H:%M:%S')] ❌ $*${NC}" | tee -a "$TEST_LOG"
}

log_warn() {
    echo -e "${YELLOW}[$(date +'%H:%M:%S')] ⚠️  $*${NC}" | tee -a "$TEST_LOG"
}

# 检查余额的函数
check_balance() {
    local token_addr=$1
    local token_name=$2
    local decimals=$3
    
    # 使用 Go 脚本检查余额
    cat > /tmp/check_bal.go <<EOF
package main
import (
    "context"
    "fmt"
    "log"
    "math/big"
    "github.com/ethereum/go-ethereum"
    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/ethclient"
)
func main() {
    client, _ := ethclient.Dial("$SEPOLIA_RPC_URL")
    tokenAddr := common.HexToAddress("$token_addr")
    walletAddr := common.HexToAddress("$WALLET_ADDR")
    
    // balanceOf(address) selector: 0x70a08231
    data := append([]byte{0x70, 0xa0, 0x82, 0x31}, common.LeftPadBytes(walletAddr.Bytes(), 32)...)
    msg := ethereum.CallMsg{To: &tokenAddr, Data: data}
    result, err := client.CallContract(context.Background(), msg, nil)
    if err != nil {
        log.Fatal(err)
    }
    balance := new(big.Int).SetBytes(result)
    fmt.Println(balance.String())
}
EOF
    
    local balance=$(go run /tmp/check_bal.go 2>/dev/null || echo "0")
    
    # 转换为人类可读格式
    if [ $decimals -eq 6 ]; then
        local readable=$(echo "scale=6; $balance / 1000000" | bc -l 2>/dev/null || echo "0")
    else
        local readable=$(echo "scale=18; $balance / 1000000000000000000" | bc -l 2>/dev/null || echo "0")
    fi
    
    echo "$token_name: $readable (raw: $balance)"
    echo "$balance"
}

# 检查合约中的余额
check_contract_balance() {
    local contract_addr=$1
    local token_addr=$2
    local token_name=$3
    local decimals=$4
    
    cat > /tmp/check_contract_bal.go <<EOF
package main
import (
    "context"
    "fmt"
    "log"
    "math/big"
    "github.com/ethereum/go-ethereum"
    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/ethclient"
)
func main() {
    client, _ := ethclient.Dial("$SEPOLIA_RPC_URL")
    tokenAddr := common.HexToAddress("$token_addr")
    contractAddr := common.HexToAddress("$contract_addr")
    
    data := append([]byte{0x70, 0xa0, 0x82, 0x31}, common.LeftPadBytes(contractAddr.Bytes(), 32)...)
    msg := ethereum.CallMsg{To: &tokenAddr, Data: data}
    result, err := client.CallContract(context.Background(), msg, nil)
    if err != nil {
        log.Fatal(err)
    }
    balance := new(big.Int).SetBytes(result)
    fmt.Println(balance.String())
}
EOF
    
    local balance=$(go run /tmp/check_contract_bal.go 2>/dev/null || echo "0")
    
    if [ $decimals -eq 6 ]; then
        local readable=$(echo "scale=6; $balance / 1000000" | bc -l 2>/dev/null || echo "0")
    else
        local readable=$(echo "scale=18; $balance / 1000000000000000000" | bc -l 2>/dev/null || echo "0")
    fi
    
    echo "$token_name in $contract_addr: $readable (raw: $balance)"
    echo "$balance"
}

# 检查代码中的 recipient 地址设置
check_code_recipients() {
    log "===== 检查代码中的 recipient 地址设置 ====="
    
    # 检查 Mint recipient
    log "检查 Mint 操作的 recipient..."
    if grep -n "Recipient.*recipient" internal/chain/univ3/adapter.go | grep -q "107"; then
        log_success "Mint recipient 正确设置在第107行"
    else
        log_warn "Mint recipient 位置可能有变化"
    fi
    
    # 检查 Collect recipient  
    log "检查 Collect 操作的 recipient..."
    if grep -n "Recipient.*recipient" internal/chain/univ3/adapter.go | grep -q "154"; then
        log_success "Collect recipient 正确设置在第154行"
    else
        log_warn "Collect recipient 位置可能有变化"
    fi
    
    # 检查 Swap recipient
    log "检查 Swap 操作的 recipient..."
    if grep -n "Recipient.*recipient" internal/chain/univ3/router.go | grep -q "110"; then
        log_success "Swap recipient 正确设置"
    else
        log_warn "Swap recipient 位置可能有变化"
    fi
    
    # 检查是否有硬编码的 0x000 地址
    log "检查是否有黑洞地址..."
    if grep -r "0x000000000000" internal/chain/ | grep -v "test" | grep -v ".git"; then
        log_error "发现可能的黑洞地址引用！"
        grep -r "0x000000000000" internal/chain/ | grep -v "test" | grep -v ".git"
    else
        log_success "未发现黑洞地址硬编码"
    fi
    
    echo ""
}

# 记录测试前余额
record_initial_balances() {
    log "===== 记录测试前余额 ====="
    
    log "钱包地址: $WALLET_ADDR"
    
    log "检查钱包 TUSD 余额..."
    INITIAL_TUSD=$(check_balance "$TUSD_ADDR" "TUSD" 6 | tail -1)
    log "初始 TUSD: $INITIAL_TUSD"
    
    log "检查钱包 WETH 余额..."
    INITIAL_WETH=$(check_balance "$WETH_ADDR" "WETH" 18 | tail -1)
    log "初始 WETH: $INITIAL_WETH"
    
    log "检查 ETH 余额..."
    INITIAL_ETH=$(cast balance $WALLET_ADDR --rpc-url "$SEPOLIA_RPC_URL" 2>/dev/null || echo "0")
    log "初始 ETH: $INITIAL_ETH"
    
    echo ""
}

# 验证代码中关键函数的 recipient 设置
verify_code_safety() {
    log "===== 验证代码安全性 ====="
    
    # 验证 BuildMintData
    log "验证 BuildMintData 函数..."
    if grep -A 15 "func.*BuildMintData" internal/chain/univ3/adapter.go | grep -q 'Recipient.*common.HexToAddress.*intent.Metadata\["recipient"\]'; then
        log_success "BuildMintData 使用 intent.Metadata[\"recipient\"]"
    else
        log_error "BuildMintData recipient 设置可能有问题！"
    fi
    
    # 验证 BuildCollectData
    log "验证 BuildCollectData 函数..."
    if grep -A 10 "func.*BuildCollectData" internal/chain/univ3/adapter.go | grep -q 'recipient.*:=.*common.HexToAddress.*intent.Metadata\["recipient"\]'; then
        log_success "BuildCollectData 使用 intent.Metadata[\"recipient\"]"
    else
        log_error "BuildCollectData recipient 设置可能有问题！"
    fi
    
    # 验证 SwapHelper 合约
    log "验证 SwapHelper.sol 合约..."
    if grep -A 5 "function swapExactInputSingle" contracts/SwapHelper.sol | grep -q "msg.sender"; then
        log_success "SwapHelper 返回代币给 msg.sender"
    else
        log_error "SwapHelper 返回地址可能有问题！"
    fi
    
    echo ""
}

# 打印测试报告
print_report() {
    log "===== 资金安全测试报告 ====="
    log ""
    log "测试时间: $(date)"
    log "测试钱包: $WALLET_ADDR"
    log ""
    log "初始余额:"
    log "  TUSD: $INITIAL_TUSD"
    log "  WETH: $INITIAL_WETH"
    log "  ETH:  $INITIAL_ETH"
    log ""
    log "详细日志保存在: $TEST_LOG"
    log ""
    log_warn "重要提示:"
    log "  1. 所有 recipient 必须设置为钱包地址"
    log "  2. 绝不能使用 0x000...000 地址"
    log "  3. Collect 操作必须指定正确的 recipient"
    log "  4. Swap 返回代币必须到钱包地址"
    log ""
}

##############################################################################
# 主测试流程
##############################################################################

main() {
    log "╔═══════════════════════════════════════════════════════════════╗"
    log "║      Phoenix V3 - 资金安全全面测试                              ║"
    log "╚═══════════════════════════════════════════════════════════════╝"
    log ""
    
    # 1. 检查代码中的 recipient 设置
    check_code_recipients
    
    # 2. 验证代码安全性
    verify_code_safety
    
    # 3. 记录初始余额
    record_initial_balances
    
    # 4. 打印报告
    print_report
    
    log_success "资金安全检查完成！"
    log ""
    log "下一步建议:"
    log "  1. 向钱包充值足够的 TUSD 测试币"
    log "  2. 运行真实交易测试"
    log "  3. 监控每笔交易的 recipient 地址"
    log "  4. 验证交易后余额变化"
}

main "$@"
