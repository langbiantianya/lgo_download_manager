#!/usr/bin/env python3
"""
pyftpdlib FTP 测试服务器（共享版）。

供 internal/protocol/protocol_test.go 中的 TestFTPLive* 测试使用。
启动后从 stdout 打印监听端口，然后阻塞运行。

用法:
    /path/to/venv/bin/python3 scripts/ftp_server.py [--port PORT] [--root DIR] [--user USER:PASS] [--timeout SECS]

默认端口 2121，默认用户 ftpuser/ftppass，默认根目录为系统临时目录。
"""

import sys
import os
import argparse
import tempfile
import shutil
from pyftpdlib.authorizers import DummyAuthorizer
from pyftpdlib.handlers import FTPHandler
from pyftpdlib.servers import FTPServer


def main():
    parser = argparse.ArgumentParser(description="pyftpdlib FTP test server (shared)")
    parser.add_argument(
        "--port", type=int, default=0,
        help="Port to bind (0 = random free port, default: 0)"
    )
    parser.add_argument(
        "--root", default=None,
        help="Root directory for FTP (default: a new temp directory)"
    )
    parser.add_argument(
        "--user", default="ftpuser:ftppass",
        help="Username:password (default: ftpuser:ftppass)"
    )
    parser.add_argument(
        "--timeout", type=int, default=300,
        help="Seconds to run before exiting (default: 300)"
    )
    args = parser.parse_args()

    # 根目录
    temp_root = False
    if args.root is None:
        root = tempfile.mkdtemp(prefix="ftp_test_root_")
        temp_root = True
    else:
        root = args.root
        os.makedirs(root, exist_ok=True)

    # 用户凭据
    try:
        username, password = args.user.split(":", 1)
    except ValueError:
        print(f"ERROR: --user must be in USER:PASS format, got: {args.user}", file=sys.stderr)
        sys.exit(1)

    authorizer = DummyAuthorizer()
    authorizer.add_user(username, password, root, perm="elradfmw")

    handler = FTPHandler
    handler.authorizer = authorizer
    handler.banner = "FTP Test Server (pyftpdlib)"

    server = FTPServer(("127.0.0.1", args.port), handler)
    actual_port = list(server.address)[1]

    # 打印端口到 stdout（Go 测试读取此值）
    print(f"{actual_port}", flush=True)

    print(
        f"# FTP server ready on 127.0.0.1:{actual_port}\n"
        f"#   user={username}  root={root}\n"
        f"#   press Ctrl+C to stop",
        file=sys.stderr
    )
    sys.stderr.flush()

    try:
        server.serve_forever(timeout=args.timeout, blocking=True)
    except KeyboardInterrupt:
        pass
    finally:
        server.close_all()
        if temp_root:
            shutil.rmtree(root, ignore_errors=True)
            print(f"# Removed temp root: {root}", file=sys.stderr)


if __name__ == "__main__":
    main()
