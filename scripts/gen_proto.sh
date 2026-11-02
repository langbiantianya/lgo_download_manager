#!/usr/bin/env bash
# 生成 protobuf 代码。生成物入库，CI 只校验同步性。
#
# 用 buf generate 而不是直接调 protoc：本机没装 protoc，而 buf 自带
# 解析器，还能顺手跑 lint（buf lint）。
#
# protoc-gen-go / protoc-gen-go-grpc 默认装在 $(go env GOPATH)/bin，
# 不一定在 PATH 上，这里显式补上。
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$(go env GOPATH)/bin:$PATH"
buf generate
gofmt -w internal/rpc/grpcx/pb
