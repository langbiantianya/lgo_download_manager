# Engine 单元测试报告

## 概述

`internal/engine/engine_test.go` 包含 18 个测试用例，覆盖 `plan()` 分片规划逻辑与端到端下载集成验证。所有测试均通过。

**测试文件**：`internal/engine/engine_test.go`
**测试命令**：`go test -v -count=1 ./internal/engine/`
**结果**：✅ 全部通过（18/18）

---

## 分片规划逻辑（plan）

### 核心规则

| 文件大小 | 分片策略 |
|---|---|
| `total ≤ 0` | 单个全流式 chunk：`{start:0, end:-1}` |
| `total < 1 MiB` | 单 chunk：`{start:0, end:total-1}`，忽略 `ResumeFrom` |
| `1 MiB ≤ total < MinChunkSize` | 按 `ChunkCount` 等分，忽略 `MinChunkSize` |
| `total ≥ MinChunkSize` | 按 `ceil(total/MinChunkSize)` 分片，`ChunkCount` 限制上限 |

---

### TestPlanTotalLTEZero ✅

**场景**：`total ≤ 0` 时生成单个全流式 chunk。

| 参数 | 值 |
|---|---|
| `total` | `-1`, `0` |
| 预期 chunks | 1 |
| 预期 chunk | `{idx:0, start:0, end:-1}` |

**验证点**：`total=-1` 和 `total=0` 均产生单个 `{start:0, end:-1}` 的流式 chunk，表示引擎从起点流式读取至文件结束。

---

### TestPlanFileBelowOneMiB ✅

**场景**：文件 `< 1 MiB` 时不分块（单 chunk），不受 `ChunkCount` 配置影响。

| 参数 | 值 |
|---|---|
| `total` | `512 KiB` |
| `ChunkCount` | `0`, `1`, `4`, `16` |
| 预期 chunks | 1 |
| 预期 chunk | `{start:0, end:512KiB-1, progress:0}` |

**验证点**：无论 `ChunkCount` 配置为多少，`< 1 MiB` 文件始终产生单个 chunk，`progress=0`。

---

### TestPlanFileBelowOneMiBWithResume ✅

**场景**：文件 `< 1 MiB` 时，`ResumeFrom` 被忽略（不产生效果）。

| 参数 | 值 |
|---|---|
| `total` | `512 KiB` |
| `ResumeFrom` | `[100 KiB]` |
| 预期 chunks | 1 |
| 预期 `progress` | `0`（忽略 ResumeFrom） |

**验证点**：`< 1 MiB` 路径中 `ResumeFrom` 被忽略，progress 始终为 0。这与 `1 MiB ≤ total < MinChunkSize` 路径的行为不同（后者会正确应用 ResumeFrom）。

---

### TestPlanFileBelowOneMiBResumeExceedsFile ✅

**场景**：文件 `< 1 MiB` 时，`ResumeFrom` 超出文件边界，被 clamp 为 0（而非 end+1）。

| 参数 | 值 |
|---|---|
| `total` | `256 KiB` |
| `ResumeFrom` | `[512 KiB]`（超出文件大小） |
| 预期 chunks | 1 |
| 预期 `progress` | `0`（ResumeFrom 在 <1MiB 路径中被忽略） |

**验证点**：`< 1 MiB` 路径由于完全忽略 ResumeFrom，progress 为 0 而非 clamp 后的 end+1。

---

### TestPlanChunkSizesOverride ✅

**场景**：显式传入 `ChunkSizes` 时完全跳过自动分片逻辑。

| 参数 | 值 |
|---|---|
| `total` | `1 GiB` |
| `ChunkSizes` | `[0,100, 200,399]` |
| `ChunkCount` | `8`（被忽略） |
| 预期 chunks | 2 |
| 预期 chunks | `[{idx:0,start:0,end:100}, {idx:1,start:200,end:399}]` |

**验证点**：`ChunkSizes` 优先级最高，`ChunkCount` 和 `MinChunkSize` 均被忽略。

---

### TestPlanFileBetweenOneMiBAndMinChunkSize ✅

**场景**：`1 MiB ≤ total < MinChunkSize` 时按 `ChunkCount` 等分，忽略 `MinChunkSize`。

| 子测试 | `total` | `MinChunkSize` | `ChunkCount` | 预期 chunks | 预期每 chunk 大小 |
|---|---|---|---|---|---|
| `2MiB_min8MiB_chunk4` | `2 MiB` | `8 MiB` | `4` | 4 | `512 KiB` |
| `3MiB_min8MiB_chunk2` | `3 MiB` | `8 MiB` | `2` | 2 | `1.5 MiB` |
| `1MiB_min8MiB_chunk1` | `1 MiB` | `8 MiB` | `1` | 1 | `1 MiB` |
| `5MiB_min10MiB_chunk5` | `5 MiB` | `10 MiB` | `5` | 5 | `1 MiB` |

**共同验证点**：

- 首 chunk 从 `start=0` 开始
- 末 chunk 到 `end=total-1` 结束
- 各 chunk 首尾相接，无间隙无重叠
- 每个 chunk 范围在 `[0, total)` 内

---

### TestPlanFileBetweenOneMiBAndMinChunkSize_ChunkCountZero ✅

**场景**：`ChunkCount=0`（或 `-1`）经 `defaults()` 映射为 `4`，再参与等分计算。

| 参数 | 值 |
|---|---|
| `total` | `3 MiB` |
| `MinChunkSize` | `8 MiB` |
| `ChunkCount` | `-1`（默认值映射为 `4`） |
| 预期 chunks | `4`（3 MiB / 4 = 0.75 MiB/chunk） |

**验证点**：`defaults()` 将 `ChunkCount ≤ 0` 映射为 `4`，然后按 `ChunkCount` 等分。

---

### TestPlanFileAboveMinChunkSize ✅

**场景**：`total ≥ MinChunkSize` 时，每个 chunk 至少为 `MinChunkSize`，由 `ChunkCount` 限制最大并发数。

| 子测试 | `total` | `MinChunkSize` | `ChunkCount` | 预期 chunks | 说明 |
|---|---|---|---|---|---|
| `10MiB_min3MiB_chunk4` | `10 MiB` | `3 MiB` | `4` | 4 | `ceil(10/3)=4` 不受限 |
| `10MiB_min2MiB_chunk4_cap` | `10 MiB` | `2 MiB` | `4` | 4 | `ceil(10/2)=5` 被 cap 为 `4` |
| `9MiB_min3MiB_chunk10` | `9 MiB` | `3 MiB` | `10` | 3 | `ceil(9/3)=3` 不受限 |
| `8MiB_min2MiB_exact` | `8 MiB` | `2 MiB` | `4` | 4 | `ceil(8/2)=4` 恰好整除 |
| `20MiB_min1MiB_chunk4_cap` | `20 MiB` | `1 MiB` | `4` | 4 | `ceil(20/1)=20` 被 cap 为 `4` |
| `4MiB_min4MiB_oneChunk` | `4 MiB` | `4 MiB` | `8` | 1 | `ceil(4/4)=1` 单 chunk |

**共同验证点**：首尾相接，无间隙无重叠，覆盖完整文件。

---

### TestPlanResumeFrom ✅

**场景**：断点续传时 `progress` 正确偏移，`ResumeFrom` 以 chunk 起点为基准。

| 参数 | 值 |
|---|---|
| `total` | `10 MiB` |
| `MinChunkSize` | `2 MiB` |
| `ChunkCount` | `4`（每 chunk `2.5 MiB`） |
| `ResumeFrom` | `[1 MiB, 0, 512 KiB, 0]` |

| chunk | 范围 | `ResumeFrom[i]` | 预期 `progress` |
|---|---|---|---|
| 0 | `[0, 2.5 MiB-1]` | `1 MiB` | `1 MiB` |
| 1 | `[2.5 MiB, 5 MiB-1]` | `0` | `chunk.start` |
| 2 | `[5 MiB, 7.5 MiB-1]` | `512 KiB` | `5 MiB + 512 KiB` |
| 3 | `[7.5 MiB, 10 MiB-1]` | `0` | `chunk.start` |

**验证点**：progress 正确应用 ResumeFrom 偏移。

---

### TestPlanResumeFromExceedsChunk ✅

**场景**：`ResumeFrom` 超出单 chunk 范围时，clamp 到 `end+1`（表示该 chunk 已完成）。

| 参数 | 值 |
|---|---|
| `total` | `10 MiB` |
| `MinChunkSize` | `2 MiB` |
| `ChunkCount` | `4` |
| `ResumeFrom` | `[5 MiB]`（超出 chunk0 范围 `[0, 2.5 MiB)`） |

**验证点**：`progress = chunk.end + 1`（已完成的标记值）。

---

### TestPlanEdgeAtOneMiB ✅

**场景**：文件恰好等于 `1 MiB` 时，走 `ChunkCount` 分片路径（不小于 `oneMiB`）。

| 参数 | 值 |
|---|---|
| `total` | `1 MiB` |
| `MinChunkSize` | `2 MiB` |
| `ChunkCount` | `4` |
| 预期 chunks | `4` |
| 预期每 chunk 大小 | `256 KiB` |

**验证点**：`total == oneMiB` 不满足 `< oneMiB` 条件，进入 `1 MiB ≤ total < MinChunkSize` 路径。

---

### TestPlanEdgeMinChunkSizeEqualsTotal ✅

**场景**：`MinChunkSize` 恰好等于文件大小时，只产生 1 个 chunk。

| 参数 | 值 |
|---|---|
| `total` | `4 MiB` |
| `MinChunkSize` | `4 MiB` |
| `ChunkCount` | `8` |
| 预期 chunks | 1 |
| 预期 chunk | `{start:0, end:4MiB-1}` |

**验证点**：`ceil(4/4)=1`。

---

### TestPlanLargeFile ✅

**场景**：超大文件（`1 GiB`）的分块计算，`ChunkCount` 限制并发上限。

| 参数 | 值 |
|---|---|
| `total` | `1 GiB` |
| `MinChunkSize` | `8 MiB` |
| `ChunkCount` | `16` |

**验证点**：`ceil(1GiB/8MiB) = 128`，被 `ChunkCount=16` 限制为 16 个 chunk。

---

## 端到端集成测试

### TestChunkedDownload ✅

**场景**：4 MiB 文件，8 分片，支持 Range 的 `httptest.Server`，验证数据完整性与进度回调。

- `ChunkCount: 8`，`ProgressEvery: 50ms`
- 服务器正确响应 `Range` 请求，返回对应字节区间
- `Content-Range`, `Accept-Ranges`, `Content-Length` 头正确设置

**验证点**：

- 下载后文件内容与原始 payload 逐字节一致
- `progressCalls > 0`（进度回调至少触发一次）
- `lastProg.DownloadedBytes == size`

---

### TestFallback ✅

**场景**：`total ≤ 0` 且 `useRange=false` 时，引擎进入 streaming fallback 模式，不使用 Range 请求。

- 服务器对 `Range` 请求返回 `416 Requested Range Not Satisfiable`
- 引擎以单流方式下载完整文件

**验证点**：文件内容与 payload 逐字节一致。

---

### TestSmallFileNoChunking ✅

**场景**：文件 `< 1 MiB` 时不分块（单 chunk），端到端验证。

| 参数 | 值 |
|---|---|
| `total` | `512 KiB` |
| `ChunkCount` | `8`（被忽略） |
| 预期 chunks | `1` |
| 预期 chunk | `{start:0, end:512KiB-1}` |

**验证点**：下载后内容一致，`job.Chunks()` 返回单个 chunk。

---

### TestMediumFileChunkCountSplit ✅

**场景**：`1 MiB ≤ total < MinChunkSize` 时按 `ChunkCount` 分片，端到端验证。

| 参数 | 值 |
|---|---|
| `total` | `2 MiB` |
| `MinChunkSize` | `8 MiB`（大于文件大小） |
| `ChunkCount` | `4` |

**验证点**：

- `job.Chunks()` 返回 4 个 chunk
- 每个 chunk 大小约为 `512 KiB`
- 下载后内容与 payload 一致

---

### TestRealDownload ✅

**场景**：对真实 HTTP 服务器（Google CDN `go1.22.3.src.tar.gz`）进行端到端下载。

- 限制前 `256 KiB` 以控制测试时间
- `ChunkCount: 4`，`ProgressEvery: 1s`
- 完整走 `Accept-Ranges` Range 请求路径

**验证点**：

- `-short` 模式自动跳过（`testing.Short()`）
- 网络不可达时跳过
- `lastProg.DownloadedBytes == totalSize`

---

## 测试矩阵汇总

| 测试 | 类型 | 覆盖规则 |
|---|---|---|
| `TestPlanTotalLTEZero` | 单元 | `total ≤ 0` |
| `TestPlanFileBelowOneMiB` | 单元 | `total < 1 MiB`，多 ChunkCount |
| `TestPlanFileBelowOneMiBWithResume` | 单元 | `< 1 MiB` + ResumeFrom |
| `TestPlanFileBelowOneMiBResumeExceedsFile` | 单元 | `< 1 MiB` + ResumeFrom 越界 |
| `TestPlanChunkSizesOverride` | 单元 | 显式 ChunkSizes |
| `TestPlanFileBetweenOneMiBAndMinChunkSize` | 单元 | 4 个子场景 |
| `TestPlanFileBetweenOneMiBAndMinChunkSize_ChunkCountZero` | 单元 | ChunkCount=0 映射 |
| `TestPlanFileAboveMinChunkSize` | 单元 | 6 个子场景 |
| `TestPlanResumeFrom` | 单元 | ResumeFrom 偏移 |
| `TestPlanResumeFromExceedsChunk` | 单元 | ResumeFrom clamp |
| `TestPlanEdgeAtOneMiB` | 单元 | 边界：total == 1 MiB |
| `TestPlanEdgeMinChunkSizeEqualsTotal` | 单元 | 边界：MinChunkSize == total |
| `TestPlanLargeFile` | 单元 | 1 GiB + ChunkCount cap |
| `TestChunkedDownload` | 集成 | 8 分片 + 进度回调 |
| `TestFallback` | 集成 | streaming fallback |
| `TestSmallFileNoChunking` | 集成 | `< 1 MiB` 端到端 |
| `TestMediumFileChunkCountSplit` | 集成 | ChunkCount 分片端到端 |
| `TestRealDownload` | 集成 | 真实 HTTP 服务器 |

**总计**：18 个测试，全部通过。
