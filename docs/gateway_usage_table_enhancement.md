# Gateway Usage 表头增强

## 目标

将 Gateway Monitor 从「模型 / 预览 / token 合计 / 延迟 / 时间」增强为可观测性表格，支持查看端点、结果、TTFT、生成速度、输入/输出/推理/缓存/总 token 等信息。

为避免 12 列同时展示导致拥挤，前端提供列显示开关；默认展示常用列，其他列可按需开启。

## 目标字段

| 字段 | 数据来源 | 说明 |
|------|---------|------|
| 时间 | `Ts` | 已有 |
| 模型 | `Model` | 已有 |
| 端点 | `Path` | 已有，当前未展示 |
| 结果 | `Status` / `Error` / `FinishReason` | 已有，当前未展示 |
| 首字延迟 | `TTFT` | 新增持久化，优先来自 Lingma finish event 的 `firstTokenDuration` |
| 总延迟 | `Latency` | 已有 |
| 生成速度 | 前端计算 `output / max((latency - ttft) / 1000, 0.001)` | 新计算 |
| 输入 | `InputTokens` | 已有，当前合并显示 |
| 输出 | `OutputTokens` | 已有，当前合并显示 |
| 推理 | `ReasoningTokens` | 新增持久化 |
| 缓存 | `CachedTokens` | 新增持久化 |
| Token 总数 | `TotalTokens` | 新增持久化，缺失时后端 fallback 为 input + output |

去掉当前表格里的「预览」列（prompt 文本），该信息在点击行后的详情弹窗中已有展示。

---

## Task 1: DB 迁移 — 新增 gateway_logs 字段

文件：`internal/storage/migrations/000003_usage.up.sql` + `000003_usage.down.sql`（新建）

up.sql:
```sql
ALTER TABLE gateway_logs ADD COLUMN cached_tokens INTEGER DEFAULT 0;
ALTER TABLE gateway_logs ADD COLUMN reasoning_tokens INTEGER DEFAULT 0;
ALTER TABLE gateway_logs ADD COLUMN total_tokens INTEGER DEFAULT 0;
ALTER TABLE gateway_logs ADD COLUMN ttft INTEGER DEFAULT 0; -- 首字延迟 ms
```

down.sql:
```sql
ALTER TABLE gateway_logs DROP COLUMN ttft;
ALTER TABLE gateway_logs DROP COLUMN total_tokens;
ALTER TABLE gateway_logs DROP COLUMN reasoning_tokens;
ALTER TABLE gateway_logs DROP COLUMN cached_tokens;
```

注意：项目使用现代 SQLite（`modernc.org/sqlite`），支持 `DROP COLUMN`。

---

## Task 2: 后端结构和 usage 解析扩展

### 2a. `internal/proto/types.go`

`Record` 和 `GatewayLog` 新增字段：

```go
CachedTokens    int   `json:"cached_tokens,omitempty" db:"cached_tokens"`
ReasoningTokens int   `json:"reasoning_tokens,omitempty" db:"reasoning_tokens"`
TotalTokens     int   `json:"total_tokens,omitempty" db:"total_tokens"`
TTFT            int64 `json:"ttft,omitempty" db:"ttft"` // 首字延迟 ms
```

### 2b. `internal/bridge/client.go`

`SSEEvent` 新增：

```go
FirstTokenDuration int // 首字延迟 ms，来自 finish event
```

finish event 解析处补充：

```go
event.FirstTokenDuration = finish.FirstTokenDuration
```

### 2c. `Usage` 解析补强

- 保持 `prompt_tokens` / `completion_tokens` 与 `input_tokens` / `output_tokens` 双向补齐。
- 支持数值字符串，例如 `"12"`。
- 如果某个 alias 是 0，继续尝试后续非零 alias，避免 `input_tokens:0` 挡住 `inputTokenCount:12`。
- 支持 OpenAI detail aliases：

```go
InputTokensDetails  *TokenDetails `json:"input_tokens_details,omitempty"`
OutputTokensDetails *TokenDetails `json:"output_tokens_details,omitempty"`
```

- 支持 Anthropic cache fields 求和：
  - `cache_read_input_tokens`
  - `cache_creation_input_tokens`

---

## Task 3: bridge handler 统一填充 usage/TTFT

涉及：

- `internal/bridge/openai_chat.go`
- `internal/bridge/openai_responses.go`
- `internal/bridge/anthropic.go`

新增统一 helper，避免各路径漏字段：

```go
func applyUsageToGatewayLog(gLog *proto.GatewayLog, usage *Usage) {
    if gLog == nil || usage == nil {
        return
    }
    usage.Consolidate()
    gLog.InputTokens = usage.PromptTokens
    gLog.OutputTokens = usage.CompletionTokens
    gLog.CachedTokens = usage.CachedTokens
    gLog.ReasoningTokens = usage.ReasoningTokens
    gLog.TotalTokens = usage.TotalTokens
}
```

所有 stream / nonStream 路径都要处理：

1. 普通 `data` event 中的 `event.Usage`。
2. 只有 `Usage`、没有 content/tool/finish_reason 的 usage-only chunk，例如：

```json
{"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}
```

3. `case "finish"` 中的：

```go
if event.Usage != nil {
    usage = event.Usage
    applyUsageToGatewayLog(gLog, usage)
}
if event.FirstTokenDuration > 0 {
    gLog.TTFT = int64(event.FirstTokenDuration)
}
```

注意：nonStream 对外不是 SSE，但内部仍然通过 `h.client.ChatStream(...)` 读取 Lingma SSE，因此 nonStream 也必须处理 `finish` event，不能假设没有 finish event。

---

## Task 4: 存储层 SQL 更新

文件：`internal/storage/sqlite.go`

- `SaveGatewayLog`：INSERT/UPDATE 新增 `cached_tokens, reasoning_tokens, total_tokens, ttft`。
- `RecentGatewayLogs`：SELECT 新增这 4 列。
- UPDATE 逻辑要和现有 token 字段一样保护已完成记录：当 `excluded.status = 0` 且已有值大于 0 时，不用中间态 0 覆盖最终统计。

---

## Task 5: 前端类型扩展

文件：`web/src/lib/types.ts`

- `TrafficRecord` / `GatewayLog` 新增：
  - `cached_tokens?`
  - `reasoning_tokens?`
  - `total_tokens?`
  - `ttft?`
- `mapGatewayLogToRecord` 补充字段映射。

---

## Task 6: 前端表格重构 + 可选列

文件：`web/src/components/GatewayMonitor.tsx`

- 去掉当前「预览」列。
- 引入列配置和显示开关，避免 12 列同时展示过于拥挤。
- 默认建议展示：时间、模型、结果、首字延迟、总延迟、速度、输入、输出、Token 总数。
- 可选展示：端点、推理、缓存。
- 表格使用横向滚动和最小宽度，token / latency / speed 右对齐。
- `processedRows.details` 补充：
  - `cachedTokens`
  - `reasoningTokens`
  - `totalTokens`
  - `ttft`
  - `path`
  - `status`
  - `error`
  - `finishReason`
  - `speed`
- 生成速度公式：

```ts
output_tokens / Math.max((latency - ttft) / 1000, 0.001)
```

- 结果列：成功显示绿色状态码，失败显示红色错误，结束原因可作为小字展示。

---

## Task 7: i18n 翻译

文件：`web/src/locales/zh.json` + `web/src/locales/en.json`

在 `monitor.table` 下新增/调整：

- `endpoint`: 端点 / Endpoint
- `result`: 结果 / Result
- `ttft`: 首字延迟 / TTFT
- `total_latency`: 总延迟 / Total Latency
- `speed`: 生成速度 / Speed
- `input`: 输入 / Input
- `output`: 输出 / Output
- `reasoning`: 推理 / Reasoning
- `cached`: 缓存 / Cached
- `total_tokens`: Token 总数 / Total Tokens
- `columns`: 列 / Columns

---

## Task 8: 验证

沙箱内优先运行不依赖真实外网/本地监听权限的测试：

```bash
GOCACHE=/private/tmp/lingma-tap-go-build go test ./internal/bridge ./internal/storage ./internal/proto ./internal/encoding ./cmd/server .
cd web && npm run build
```

`go test ./...` 可作为本机完整验证，但注意：

- `internal/api` 测试会监听本地端口，受沙箱权限影响。
- `internal/auth` 部分测试会访问真实 Lingma 网络接口，可能涉及本地凭据副作用。

Wails 生成文件：`web/wailsjs/wailsjs/go/models.ts` 由 `wails generate` / `wails build` 生成，若未运行生成流程不要手改。
