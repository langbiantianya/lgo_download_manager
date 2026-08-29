#!/usr/bin/env python3
"""
pyftpdlib FTP 测试服务器（独立版）。

用法:
    /path/to/venv/bin/python3 ftp_server.py [--port PORT] [--root DIR] [--user USER:PASS] [--timeout SECS]

默认端口 2121，默认用户 ftpuser/ftppass，默认根目录为系统临时目录。
启动后将端口号打印到 stdout，然后阻塞运行。

注意: 优先使用 scripts/ftp_server.py（共享版），它与 Go 测试集成。
本脚本适合手动调试或独立验证 FTP 服务器行为。
"""

import sys
import os
import argparse
import tempfile
import time

from pyftpdlib.authorizers import DummyAuthorizer
from pyftpdlib.handlers import FTPHandler
from pyftpdlib.servers import FTPServer


def main():
    parser = argparse.ArgumentParser(description="pyftpdlib FTP test server (standalone)")
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
    root = args.root
    if root is None:
        root = tempfile.mkdtemp(prefix="ftp_test_root_")
        print(f"# Created temp root: {root}", file=sys.stderr)
    else:
        os.makedirs(root, exist_ok=True)

    # 用户凭据
    try:
        username, password = args.user.split(":", 1)
    except ValueError:
        print(f"ERROR: --user must be in USER:PASS format, got: {args.user}", file=sys.stderr)
        sys.exit(1)

    authorizer = DummyAuthorizer()
    authorizer.add_user(username, password, root, perm="elradfmw")
    # 可选：添加匿名用户
    # authorizer.add_anonymous(root, perm="elr")

    handler = FTPHandler
    handler.authorizer = authorizer
    handler.banner = "FTP Test Server (pyftpdlib)"

    server = FTPServer(("127.0.0.1", args.port), handler)
    actual_port = list(server.address)[1]

    # 打印端口到 stdout
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
        if args.root is None:
            import shutil
            shutil.rmtree(root, ignore_errors=True)
            print(f"# Removed temp root: {root}", file=sys.stderr)


if __name__ == "__main__":
    main()
