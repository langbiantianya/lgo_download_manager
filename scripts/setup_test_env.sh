#!/bin/bash
#
# scripts/setup_test_env.sh
#
# 初始化 lgo_download_manager 项目所需的测试依赖环境。
#
# 当前依赖:
#   - Python 3 + pyftpdlib（用于 FTP 协议测试）
#
# 用法:
#   ./scripts/setup_test_env.sh [--check] [--verbose]
#
#   --check     仅检查环境是否就绪，不创建。
#   --verbose   显示 pip 安装详情。
#
# 虚拟环境路径: /tmp/ftp_pytest_venv
#
# 注意: 如需修改虚拟环境路径，请同时修改:
#   1. internal/protocol/protocol_test.go 中的 pythonVenv 变量
#   2. scripts/ftp_test_server/setup.sh 中的 VENV_DIR
#

set -euo pipefail

VENV_DIR="${VENV_DIR:-/tmp/ftp_pytest_venv}"
CHECK_ONLY=false
VERBOSE=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --check)   CHECK_ONLY=true; shift ;;
        --verbose) VERBOSE=true; shift ;;
        *)         echo "Unknown option: $1"; exit 1 ;;
    esac
done

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_ok()  { echo -e "${GREEN}[OK]${NC} $1"; }
log_warn(){ echo -e "${YELLOW}[WARN]${NC} $1"; }
log_err() { echo -e "${RED}[ERR]${NC} $1"; }
log_info(){ echo "==> $1"; }

# ---------------------------------------------------------------------------
# 检查 Python 3
# ---------------------------------------------------------------------------
check_python() {
    if ! command -v python3 &>/dev/null; then
        log_err "python3 not found"
        return 1
    fi
    local ver
    ver=$(python3 --version 2>&1)
    log_ok "$ver"
    return 0
}

# ---------------------------------------------------------------------------
# 检查 pyftpdlib 是否可用
# ---------------------------------------------------------------------------
check_pyftpdlib() {
    if python3 -c "import pyftpdlib" 2>/dev/null; then
        log_ok "pyftpdlib installed"
        return 0
    else
        log_warn "pyftpdlib not installed"
        return 1
    fi
}

# ---------------------------------------------------------------------------
# 检查虚拟环境
# ---------------------------------------------------------------------------
check_venv() {
    if [[ -d "$VENV_DIR" && -f "$VENV_DIR/bin/python3" ]]; then
        if "$VENV_DIR/bin/python3" -c "import pyftpdlib" 2>/dev/null; then
            log_ok "$VENV_DIR (pyftpdlib installed)"
            return 0
        else
            log_warn "$VENV_DIR exists but pyftpdlib not installed"
            return 1
        fi
    fi
    log_warn "$VENV_DIR does not exist"
    return 1
}

# ---------------------------------------------------------------------------
# 创建虚拟环境
# ---------------------------------------------------------------------------
create_venv() {
    log_info "Creating Python virtual environment at $VENV_DIR"
    python3 -m venv "$VENV_DIR"
    log_ok "Virtual environment created"
}

# ---------------------------------------------------------------------------
# 安装 pyftpdlib
# ---------------------------------------------------------------------------
install_pyftpdlib() {
    log_info "Installing pyftpdlib..."
    local pip="$VENV_DIR/bin/pip"
    if [[ "$VERBOSE" == true ]]; then
        "$pip" install pyftpdlib
    else
        "$pip" install --quiet pyftpdlib
    fi
    log_ok "pyftpdlib installed"
}

# ---------------------------------------------------------------------------
# 主流程
# ---------------------------------------------------------------------------
main() {
    echo "============================================"
    echo " lgo_download_manager 测试环境初始化"
    echo "============================================"
    echo ""

    # 检查 Python
    log_info "Checking Python 3..."
    if ! check_python; then
        echo ""
        log_err "Python 3 is required but not found."
        echo "   请安装 Python 3 后重试。"
        exit 1
    fi

    echo ""
    echo "--- 检查模式 ---"
    if [[ "$CHECK_ONLY" == true ]]; then
        local ok=true

        echo ""
        log_info "检查虚拟环境..."
        if ! check_venv; then
            ok=false
        fi

        echo ""
        if [[ "$ok" == true ]]; then
            log_ok "所有检查通过"
            exit 0
        else
            log_err "环境未就绪，请运行不带 --check 的命令进行初始化"
            exit 1
        fi
    fi

    # 创建/更新虚拟环境
    echo ""
    echo "--- 初始化 ---"

    if [[ -d "$VENV_DIR" ]]; then
        log_info "虚拟环境已存在: $VENV_DIR"
        log_info "更新 pyftpdlib..."
        install_pyftpdlib
    else
        create_venv
        install_pyftpdlib
    fi

    echo ""
    echo "============================================"
    echo " 初始化完成"
    echo "============================================"
    echo ""
    echo "  虚拟环境: $VENV_DIR"
    echo ""
    echo "  运行 FTP 测试:"
    echo "    VENV_DIR=$VENV_DIR go test -v -run 'TestFTPLive' ./internal/protocol/"
    echo ""
    echo "  手动启动 FTP 服务器:"
    echo "    $VENV_DIR/bin/python3 \$(pwd)/scripts/ftp_server.py"
    echo ""
    echo "  验证虚拟环境:"
    echo "    $VENV_DIR/bin/python3 -c 'import pyftpdlib; print(\"pyftpdlib ok\")'"
    echo ""
    echo "  如需修改虚拟环境路径，请同时更新:"
    echo "    - internal/protocol/protocol_test.go 中的 pythonVenv"
    echo "    - scripts/ftp_test_server/setup.sh 中的 VENV_DIR"
}

main
