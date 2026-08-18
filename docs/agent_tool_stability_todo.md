# Agent 工具调用稳定性 TODO

- **日期：** 2026-08-16
- **状态：** 待排期
- **范围：** `/v1/responses`、`/v1/messages`、`/v1/chat/completions` 到 Lingma 上游的上下文治理、工具事务完整性、流式恢复和并发背压
- **证据基线：** [agent_endpoint_stability_analysis.md](agent_endpoint_stability_analysis.md)

## 1. 目标

在不改变三种北向协议语义的前提下，降低深工具历史、大型工具 schema、超长工具输出和上游流式故障导致的失败率。

期望最终形成以下链路：

```text
Responses / Anthropic Messages / Chat Completions
                         │
                         ▼
                Canonical Turn IR
                         │
             validate + redact + budget
                         │
       keep / summarize / spill atomic groups
                         │
                         ▼
                  Lingma request body
                         │
          pre-commit retry + profile recovery
                         │
                         ▼
                protocol-native SSE output
```

本计划不追求通过固定阈值消除所有上游不稳定。已有证据表明，失败与完整上下文状态、工具交互轮次和上游 profile 的组合有关，不能简单归结为请求字符数或工具数量。

## 2. 已有能力

- [x] 三种北向协议共享 Lingma 上游调用层。
- [x] `/v1/responses` 支持 `function_call`、`function_call_output` 和原生 Responses SSE 映射。
- [x] 未向客户端提交可行动输出前，可对 HTTP 408/425/429/5xx、EOF、HTTP/2 stream error、连接 reset/timeout 和可恢复 SSE error 重试。
- [x] 重试重新生成 request/business ID，保留 session，并遵守 `Retry-After`。
- [x] 对符合条件的大型 reasoning 请求支持首个可行动输出 watchdog 和 profile recovery。
- [x] `/v1/responses` 当前具备 512 KiB 的上下文裁剪和当前 turn 保护。
- [x] GatewayLog 已记录重试次数、错误分类、首个可行动输出时间和 recovery profile，不记录 prompt、工具结果或凭据。

相关实现：

- `internal/bridge/openai_responses.go`
- `internal/bridge/responses_context.go`
- `internal/bridge/upstream_retry.go`
- `internal/bridge/thinking_fallback.go`

## 3. 必须保持的设计约束

- Codex 继续使用 Responses，Claude Code 继续使用 Anthropic Messages；不能要求客户端退化到近似兼容的 Chat Completions。
- 当前请求及当前 turn 必须保持原始语义。若其自身已经超过硬限制，返回明确错误，不能静默删除或截断用户意图。
- 一条 assistant 消息中的所有 tool calls 及其对应 tool results 是一个原子事务组，只能整体保留、整体摘要或整体删除。
- 重试只允许发生在下游尚未看到可行动输出时。已经提交 content 或 tool call 后不得自动重放。
- 不在代理层拆分一个 agent turn；拆分会破坏 call ID、工具依赖、计费边界和流式状态机。
- 多模态 content block、custom/freeform tool 和未知协议字段默认透传；不能先转成纯文本再尝试恢复。
- 所有摘要、spill 和日志在处理前必须脱敏。不得持久化 auth header、cookie、token、原始凭据或未脱敏请求。
- 新策略必须 feature-gated，并保留关闭后回到当前行为的路径。

## 4. P0：先修复工具事务完整性

### 4.1 建立原子 interaction group

- [ ] 定义内部最小结构，至少覆盖：
  - system/developer guidance；
  - 普通 user/assistant turn；
  - assistant tool-call batch；
  - 与 batch 对应的全部 tool results；
  - 图片、文件和其他结构化 content part；
  - 当前请求边界及来源协议。
- [ ] 使用 call ID 构建 Tool Ledger，验证每个历史 tool result 都有对应 call。
- [ ] 并行工具调用必须保持原 assistant 消息和全部结果的顺序，不得逐条倒序拼接。
- [ ] 缺少部分结果的历史 batch 按“不完整事务”处理：保留当前 turn 中的原始内容；旧历史中则整体跳过或生成明确的不完整摘要。
- [ ] 禁止对历史 tool arguments 和 tool results 分别做互不关联的截断。

### 4.2 修复现有 Responses trimmer

- [ ] 为 `responses_context.go` 增加一个 assistant 发出 2 个及以上并行 tool calls 的回归测试。
- [ ] 覆盖结果顺序不同、缺少一个结果、孤立 tool result、重复 call ID、超大 batch 等情况。
- [ ] 修复倒序收集导致 `tool result -> assistant call -> tool result` 的非法顺序。
- [ ] 修复后保持现有“当前 turn 自身超限则返回 `ErrCurrentRoundTooLarge`”语义。

### 4.3 验收条件

- [ ] 任何投影后的历史均满足：assistant tool call 先于对应 result，且没有孤立 result。
- [ ] 一个并行 batch 不会被部分保留。
- [ ] Responses、Messages 和 Chat 的相同语义输入生成等价 Tool Ledger。
- [ ] 现有 `go test ./...` 全部通过。

## 5. P1：统一 Canonical Turn IR 与预算器

### 5.1 协议适配边界

- [ ] 分别实现 Responses、Anthropic Messages 和 Chat Completions 到 Canonical Turn IR 的无损转换。
- [ ] 投影逻辑只作用于 IR，不在三个 handler 内分别维护不同的截断规则。
- [ ] IR 到 Lingma body 的转换保留 `tool_choice`、并行工具语义、多模态内容和必要的 reasoning 元数据。
- [ ] 对未知 content/tool 类型采用显式透传或明确的 unsupported error，禁止静默丢弃。

### 5.2 模型级预算

- [ ] 建立按模型和 profile 配置的输入预算，至少同时计算：
  - 估算 input tokens；
  - 编码后的 body bytes；
  - 工具定义大小；
  - tool-call/result batch 数量；
  - 当前 turn 大小。
- [ ] token 估算器对中文、英文和 JSON 分别校准；`chars/4` 只能作为缺少 tokenizer 时的保守 fallback。
- [ ] 为输出 tokens、协议包装和上游安全余量预留预算，不能把整个模型窗口分配给输入。
- [ ] 当前 turn、强制 guidance 和必需工具 schema 本身超限时返回明确的 4xx `context_too_large`，并记录不含内容的尺寸指标。

### 5.3 分层投影顺序

- [ ] 第一层：保留当前 turn 和最近的完整 interaction groups。
- [ ] 第二层：对旧的已完成工具事务生成结构化摘要，保留工具名、状态、关键产物、错误类别和内容摘要哈希。
- [ ] 第三层：压缩更早的普通对话，同时保留最近用户目标和仍然有效的约束。
- [ ] 第四层：选择性精简工具 schema，优先删除 examples/defaults/重复说明，保留简短 description、required、enum 和关键字段语义。
- [ ] 投影完成后重新计算完整预算；任何扩展 tool context 的步骤都不得绕过最终预算检查。
- [ ] 对相同历史生成稳定、确定性的摘要，避免每轮改写旧前缀而破坏 prompt cache。

### 5.4 Harness 处理规则

- [ ] 不使用“命中 marker 就删除整条消息”的规则。
- [ ] 对混合了 AGENTS/环境信息和真实请求的消息进行分段，真实用户意图必须保留。
- [ ] 长用户消息采用结构边界或头尾保留；不能只保留前 N 个字符。
- [ ] repository instructions 只有在确实保留或被可靠摘要后，才允许在基础 system prompt 中声称会遵循它们。
- [ ] marker 字符串出现在代码块、引用或待分析文本中时，不得被当作 harness 删除。

### 5.5 验收条件

- [ ] 三个入口共享同一预算和投影结果，不再只有 Responses 获得上下文保护。
- [ ] 投影前后记录 messages/tools/body bytes、估算 tokens、保留/摘要/丢弃 group 数量，但不记录内容。
- [ ] 同一输入重复投影得到字节级稳定结果。
- [ ] 关闭 feature flag 后与当前请求构造行为兼容。

## 6. P1：流式协议与恢复补强

- [ ] 明确识别 Lingma 上游的所有正常终止形态；EOF 未出现有效终止信号时不得伪造成功 `[DONE]`。
- [ ] 识别 HTTP 200 SSE 中的 provider error、429 envelope 和 `event:error`。
- [ ] 将 ping、metadata、usage 和 reasoning-only 与“可行动输出”区分，避免无意义 frame 阻止安全重试。
- [ ] 保留当前 pre-commit retry 原则，并增加以下回归测试：
  - 第一个 content 之前 EOF，可重试且客户端不看到重复事件；
  - tool-call delta 提交之后 EOF，不重试；
  - reasoning-only watchdog recovery 不泄露第一次 attempt；
  - HTTP 429 和流内 429 都映射正确；
  - `Retry-After` 和取消信号生效；
  - 完整终止、截断 EOF、客户端取消三者可观测且不会混淆。
- [ ] 如为某个客户端加入 streaming 到 non-streaming fallback，必须限定为尚未提交可行动输出，并单独 feature-gate。

## 7. P2：Tool Result Spill、缓存与脱敏

### 7.1 Spill 设计

- [ ] 定义 spill 阈值和内容寻址格式，例如 `sha256 + byte length + media type + redacted preview`。
- [ ] 超大工具输出先脱敏，再保存到应用数据目录；对话只保留摘要和不可猜测的引用 ID。
- [ ] 只有存在受控的重新读取能力时才称为 spill；否则应标记为不可恢复摘要。
- [ ] 设置单文件、单会话、全局磁盘配额以及 TTL/清理策略。
- [ ] 路径不可由模型直接构造，读取接口必须防止目录穿越和跨会话访问。
- [ ] auth 文件、请求 header、SQLite/WAL、HAR 和已知 secret 类型默认禁止 spill。

### 7.2 去重与缓存

- [ ] 对完全相同且声明为可缓存的只读工具调用使用内容哈希去重。
- [ ] 缓存键包含工具版本、参数规范化结果、工作区/会话边界和相关文件版本信息。
- [ ] 写操作、终端交互和具有外部副作用的工具默认不缓存。
- [ ] 缓存命中仍生成独立 tool result 事件，并保持原 call ID。

### 7.3 验收条件

- [ ] 大输出不再进入后续每个请求的完整上下文。
- [ ] 模型可在需要时通过受控工具取回 spill 的指定片段。
- [ ] secret fixture 不会出现在摘要、spill 索引、日志或测试快照中。
- [ ] 进程重启和 TTL 清理后不会留下无界孤儿文件。

## 8. P2：并发背压与限流

- [ ] 在共享 Lingma 上游层增加全局 semaphore，而不是在三个协议 handler 中分别限流。
- [ ] 初始默认并发建议为 4，必须允许配置，并设置合理上限。
- [ ] 使用有界等待队列；队列满时返回明确的 429/503 和 `Retry-After`，不能无限创建 goroutine。
- [ ] 客户端取消时立即从队列移除。
- [ ] 按账户、模型/profile 和全局维度记录 in-flight、queue depth、queue wait、rejected、429、attempts。
- [ ] 不在没有账户池证据前实现复杂的“账号数 × 固定倍数”自动公式。

## 9. 测试矩阵

### 协议与内容

- [ ] 用户消息同时包含 harness、代码块和尾部真实请求，真实请求仍被保留。
- [ ] 超过 3.2K 字符且请求位于尾部的 user message 不丢失意图。
- [ ] Responses custom/freeform tool，例如自由格式 patch 输入，不被过滤。
- [ ] 图片、文件、text 混合 content block 在保守路径中保持结构。
- [ ] Anthropic user block 同时包含 `tool_result` 和 text 时，上游顺序合法。
- [ ] Responses 连续多个 `function_call` 聚合为一个 assistant batch，随后结果顺序正确。

### 预算与投影

- [ ] 旧普通历史、单工具事务、并行工具事务分别触发预算边界。
- [ ] system + 当前 turn + 必需 tools 自身超限时返回明确错误。
- [ ] 摘要或 tail expansion 后再次超限时继续收敛或失败，不越过硬限制。
- [ ] 中文、英文、大 JSON、base64、多行终端输出分别验证估算偏差。
- [ ] 投影不修改原请求对象，失败和重试不会叠加重复摘要。

### 流式与负载

- [ ] HTTP 200 后收到流内错误。
- [ ] 无 `[DONE]` 的 EOF。
- [ ] 长时间 reasoning-only、有心跳但无 actionable output、完全静默三种情况分别处理。
- [ ] 429 burst、5xx burst、队列超载和客户端批量取消。
- [ ] 并发工具调用不会重复执行或错配 call ID。
- [ ] `go test ./...`、前端构建和必要的脱敏 E2E 重放通过。

## 10. 可观测性与上线方式

- [ ] 新增不含内容的投影指标：`projection_mode`、`original/projected_bytes`、`estimated_tokens`、`groups_kept/summarized/dropped`、`spill_bytes`、`schema_bytes_saved`。
- [ ] 新增背压指标：`upstream_inflight`、`queue_depth`、`queue_wait_ms`、`queue_rejected`。
- [ ] 区分 `context_too_large`、`invalid_tool_ledger`、`projection_failed`、`upstream_truncated` 和已有传输错误类别。
- [ ] 先以 observe-only 模式计算投影结果但不改请求，收集真实尺寸分布。
- [ ] 再对单一协议或模型灰度启用，保留快速关闭开关。
- [ ] 比较启用前后的成功率、首个可行动输出时间、工具完成率、重试率、429 和平均输入大小。
- [ ] 未完成受控 E2E 验证前，不全局启用自动 schema 精简、历史摘要或 profile 改写。

## 11. 明确不做

- [ ] 不把 Codex 或 Claude Code 强制改成 Chat Completions。
- [ ] 不按固定“最近 10 条消息”直接截断。
- [ ] 不因任意 harness marker 删除整条用户消息。
- [ ] 不静默丢弃不认识的工具或多模态 block。
- [ ] 不在已经输出 content/tool call 后自动重试。
- [ ] 不拆分单个 agent turn，也不合并相互依赖的请求。
- [ ] 不把 `agent_common` 或 non-reasoning profile 当成所有请求的默认修复。

## 12. 建议实施拆分

1. **PR 1：Tool Ledger 与并行工具事务回归修复**
   - 只修结构完整性，不引入摘要和 spill。
2. **PR 2：Canonical Turn IR 与 observe-only budget report**
   - 三种协议统一进入 IR，生产请求仍保持原样。
3. **PR 3：原子历史裁剪**
   - feature-gated，替代 Responses 当前逐消息 trimmer，并覆盖三个入口。
4. **PR 4：确定性历史摘要和安全 schema slimming**
   - 先处理旧的完整事务，保留稳定前缀。
5. **PR 5：Tool Result Spill 与脱敏读取工具**
   - 带磁盘配额、TTL、访问边界和清理测试。
6. **PR 6：全局并发 semaphore、队列和指标**
   - 独立于投影功能上线，便于回滚和评估。

## 13. 社区实现参考

这些仓库用于吸收设计思路，不表示其实现可直接复制。实施前需重新确认最新代码、许可证和实际协议行为。

- [ZipperCode/lingma2api：Canonical request token 估算和按模型裁剪](https://github.com/ZipperCode/lingma2api/blob/795bbd14dfb599f0b4915edfd08659387e52176c/internal/proxy/truncation.go#L77-L164)
- [Liki4/qodercli2api：HTTP/队列重试与 Retry-After](https://github.com/Liki4/qodercli2api/blob/14e13e1dedbff44706a44b24857cea03957f0ce5/proxy.go#L294-L347)
- [Liki4/qodercli2api：Anthropic tool_result 邻接顺序](https://github.com/Liki4/qodercli2api/blob/14e13e1dedbff44706a44b24857cea03957f0ce5/convert.go#L161-L194)
- [EchoPing07/Qoder-2API-Go：SSE 终止和流内错误验证](https://github.com/EchoPing07/Qoder-2API-Go/blob/2cc759aa6a04c58d8266d6c7d270f90f9e9a9dee/auth/auth.go#L557-L807)
- [Lutiancheng1/lingma-proxy：提交前模型 fallback](https://github.com/Lutiancheng1/lingma-proxy/blob/ede45575260a1e66c4c43fb2d767de05b1e02958/internal/service/service.go#L418-L539)
- [foxy1402/qoder-proxy：最近 10 条截断示例及其局限](https://github.com/foxy1402/qoder-proxy/blob/b6facd03be30f2ac114a4c87b2cd42ccef302884/src/helpers/format.js#L241-L269)
- [cubk1/qoder2api：早期 SSE 读取实现](https://github.com/cubk1/qoder2api/blob/449f9741f1b49c81f561c4b8c58ae1ea8ed0666e/src/main/java/us/cubk/BearerApiClient.java#L76-L118)
- [jyao0708/qoder2api：Responses 协议转换](https://github.com/jyao0708/qoder2api/blob/0b17d787742b383b01cf81f2ebcf41c14a4055a0/internal/bridge/responses.go)
