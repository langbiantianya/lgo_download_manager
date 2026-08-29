#!/bin/bash
#
# scripts/ftp_test_server/setup.sh
#
# 为 internal/protocol/protocol_test.go 中的 TestFTPLive* 测试准备 Python 虚拟环境。
#
# 用法:
#   ./scripts/ftp_test_server/setup.sh [--check]
#
#   --check   仅检查虚拟环境是否存在并包含 pyftpdlib，不创建。
#
# 创建的虚拟环境路径: /tmp/ftp_pytest_venv
#
# 如需在非默认路径创建，请同时修改:
#   1. internal/protocol/protocol_test.go 中的 pythonVenv 变量
#   2. scripts/ftp_test_server/ftp_server.py 中的 venv 路径
#

set -euo pipefail

VENV_DIR="${VENV_DIR:-/tmp/ftp_pytest_venv}"
CHECK_ONLY=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --check) CHECK_ONLY=true; shift ;;
        *)       echo "Unknown option: $1"; exit 1 ;;
    esac
done

if [[ "$CHECK_ONLY" == true ]]; then
    if [[ -d "$VENV_DIR" && -f "$VENV_DIR/bin/python3" ]]; then
        if "$VENV_DIR/bin/python3" -c "import pyftpdlib" 2>/dev/null; then
            echo "OK: $VENV_DIR (pyftpdlib installed)"
            exit 0
        else
            echo "WARN: $VENV_DIR exists but pyftpdlib is not installed"
            exit 1
        fi
    else
        echo "WARN: $VENV_DIR does not exist or is incomplete"
        exit 1
    fi
fi

echo "==> Creating Python virtual environment at $VENV_DIR"
python3 -m venv "$VENV_DIR"

echo "==> Installing pyftpdlib"
"$VENV_DIR/bin/pip" install --quiet pyftpdlib

echo ""
echo "==> Setup complete: $VENV_DIR"
echo ""
echo "   To start the FTP test server manually:"
echo "     $VENV_DIR/bin/python3 $(dirname "$0")/ftp_server.py"
echo ""
echo "   To run the live FTP tests:"
echo "     go test -v -run 'TestFTPLive' ./internal/protocol/"
echo ""
echo "   To verify the venv:"
echo "     $VENV_DIR/bin/python3 -c 'import pyftpdlib; print(pyftpdlib.__version__) if hasattr(pyftpdlib, \"__version__\") else print(\"ok\")'"
