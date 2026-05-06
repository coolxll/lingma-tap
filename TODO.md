# TODO: Refactoring & Maintenance

## 1. 替换手工 MITM 代理实现
- **涉及文件**：`internal/proxy/server.go`, `internal/mitm/interceptor.go`, `internal/mitm/detect.go`, `internal/ca/ca.go`
- **问题**：手工处理 TCP CONNECT 隧道、TLS 握手、SNI 嗅探以及底层的 HTTP 请求/响应转发，容易遗漏边界情况（如 Chunked 编码、连接复用），导致难以排查的网络问题或内存泄漏。
- **解决方案**：引入成熟的代理拦截库，如 `github.com/elazarl/goproxy` 或 `github.com/lqqyt2423/go-mitmproxy`。

## 2. 引入标准 SSE 流解析库
- **涉及文件**：`internal/proto/sse.go`, `internal/bridge/client.go`
- **问题**：当前通过 `strings.Split(body, "\n\n")` 手工解析 Server-Sent Events 流。这种做法无法安全应对由于网络原因导致的 TCP 数据包半截断问题，健壮性较弱。
- **解决方案**：替换为标准且严谨的 SSE 解析库，如 `github.com/r3labs/sse`、`github.com/tmaxmax/go-sse` 或 `github.com/launchdarkly/eventsource`。

## 3. 优化数据库访问层与表结构迁移
- **涉及文件**：`internal/storage/sqlite.go`
- **问题**：大量手写的原生 SQL（建表、修改字段、UPSERT）和繁琐的 `rows.Scan` 操作。随着后续数据字段的增加，代码维护成本极高且易错。
- **解决方案**：引入 ORM 框架如 `gorm`，或者使用 `github.com/jmoiron/sqlx` 简化结果映射，同时使用 `github.com/golang-migrate/migrate` 规范化管理表结构版本升级。

## 4. 替换 UUID 生成实现
- **涉及文件**：`internal/bridge/client.go` (`newUUID` 方法)
- **问题**：手工使用 `crypto/rand` 和位运算实现了一个极简版 UUID，缺乏标准性。
- **解决方案**：直接使用 `github.com/google/uuid` (目前已存在于项目 `go.mod` 的 indirect 依赖中)，调用 `uuid.New().String()` 进行替换。
