# Lingma Agent Endpoint Stability Evidence

- **日期：** 2026-07-12
- **状态：** E2E 证据附录；不构成全局路由或计费结论
- **主文档：** [lingma_mitm_protocol_parity.md](lingma_mitm_protocol_parity.md)

## 1. 范围与证据边界

本文记录 3145、3202 的受控重放、原生客户端复现和一次跨模型对照，用于解释 Lingma Agent 端点的失稳模式。

文档不记录 prompt、tool result、认证信息、真实 session ID、兼容中继地址或临时数据路径。结果分为：

| 标记 | 含义 |
|------|------|
| 已复现 | 在受控重放或原生客户端中观察到 |
| 强证据 | 可排除一部分假设，但仍存在协议差异等混杂因素 |
| 未证实 | 不应直接转化为生产策略 |

## 2. 端点语义

`/sse/agent_chat_generation` 的一个 HTTP 请求应视为一次 Agent turn，而不是普通裸 LLM completion：它可以输出长 reasoning、决定工具调用并产生最终内容；原生客户端在一轮结束后还可能携带更新后的上下文继续发起后续 turn。

因此，用户看到的长时间卡住可能来自单个 Agent turn 的 reasoning-only 行为，也可能被客户端后续编排放大。SLA 策略应以“首个可行动输出”和“是否最终完成”为判断点，而不是只看 HTTP 连接是否已经建立。

## 3. 3145：深工具历史后的 HTTP/2 错误

### 3.1 消减结果

3145 与成功的 3143 前 32 条 messages 和 tools 结构相同，只增加了一轮 assistant tool-call 和两个 tool result。

- 清空工具参数和结果正文，错误仍出现。
- 移除顶层 tools、随机化 request/session/business ID，错误仍出现。
- 删除完整的新增工具轮次后，约 3.9 秒正常完成。

**已复现结论：** 触发因素更接近“深工具调用历史 + 再增加一整轮工具交互 + 特定上游 profile”的组合，而不是工具文本、参数或标识符。约 18 个 tool results 只是风险信号，不是绝对阈值。

### 3.2 route profile 矩阵

| profile | task_id | 结果 |
|---------|---------|------|
| `agent_chat + reasoning + source=system` | `question_refine` | 约 67.2 秒 HTTP/2 `INTERNAL_ERROR` |
| 仅改为 `agent_common` | `question_refine` | 部分输出后约 35.3 秒 `unexpected EOF` |
| `agent_common + reasoning=false + source=""` | `question_refine` | 约 12.5 秒正常 `[DONE]` |
| `agent_chat + true + system` | `common` | 约 66.1 秒 HTTP/2 `INTERNAL_ERROR` |
| `agent_common + true + system` | `common` | 约 64.6 秒 HTTP/2 `INTERNAL_ERROR` |
| `agent_common + false + system` | `common` | 约 64.7 秒 HTTP/2 `INTERNAL_ERROR` |
| `agent_common + false + source=""` | `common` | 约 81.7 秒 HTTP/2 `INTERNAL_ERROR` |

**结论：** 完整三元组在这个 `question_refine` 样本中优于原始 route，但在 `common` 下没有成功。它是 profile 相关的降级候选，不是任意请求的确定性修复。

## 4. 3202：reasoning-only 卡住

3202 在约 90 秒内只有 reasoning，没有 content、tool-call 或完成信号。reasoning 表现为反复自我校验和推翻先前判断，而不是正常的长分析；将 `max_tokens` 从 16384 降到 4096 也未可靠结束该 turn。

替换单条 user、assistant tool-call 或 tool result 均无法解除循环；替换完整成功上下文并生成新会话后可在约 5.6 秒发出工具调用。

| profile | task_id | 结果 |
|---------|---------|------|
| `agent_chat + reasoning + source=system` | `question_refine` | 90 秒 reasoning-only，无完成 |
| 仅改为 `agent_common` | `question_refine` | 约 2.0 秒正常 tool call |
| `agent_common + reasoning=false + source=""` | `question_refine` | 约 2.2 秒正常完成 |
| `agent_chat + reasoning + source=system` | `common` | 约 88.1 秒完成，其中约 84.5 秒 reasoning-only |
| `agent_common + true + system` | `common` | 95 秒 reasoning-only 超时 |
| `agent_common + false + system` | `common` | 95 秒 reasoning-only 超时 |
| `agent_common + false + source=""` | `common` | 约 1 秒空流 EOF |

**已复现结论：** reasoning 卡住由完整上下文状态组合触发，`max_tokens` 不是可靠的上游 reasoning budget 控制；`agent_common` 不在所有 `task_id` 下更稳定。

## 5. 原生客户端与跨模型对照

将同一对话序列在原生客户端重走时，也观察到长 reasoning 和超时。原生客户端的多个后续 turn 仍来自同一 Agent 端点。

这是“上游 Agent 行为至少是问题的一部分”的强证据，但不能排除 bridge 的 `task_id`、模型元数据和会话关系差异会额外影响结果。

同一 3145 结构在一次第三方 OpenAI 兼容模型服务对照中可由不同模型正常完成。它只能说明请求内容并非必然不可处理；这是单次对照，不代表第三方模型、兼容中继或任何聚合平台的长期可用性与 SLA。

## 6. 可成立与不可成立的工程结论

| 结论 | 状态 | 原因 |
|------|------|------|
| `task_id` 必须进入 route profile | 可成立 | 相同 Agent/模型配置在不同 `task_id` 下出现不同结果 |
| `agent_common` 应全局替代 `agent_chat` | 不成立 | 3145/3202 的 `common` 变体均未显示这种优势 |
| 出现 EOF/504 后可无条件换三元组重试 | 未证实 | 有效样本只覆盖特定 `question_refine` profile，且可能改变语义和计费归因 |
| 深工具历史可按固定阈值预路由 | 不成立 | 观察到的是风险升高，不是绝对阈值 |
| 上游存在独立的不稳定分支 | 可成立 | 原生客户端可出现同类卡住，第三方对照可处理同一结构 |
| `task_id` 会改变计费 | 未证实 | 模型和价格字段同时变化，缺少账户侧对照 |

## 7. 现有 fallback 与缺口

当前 `thinking_fallback.go` 已经实现：

```text
agent_id:      agent_chat  -> agent_common
is_reasoning:  true        -> false
source:        system      -> ""
```

它仅面向 `gm51model` 的大 reasoning 请求：body 至少 128 KiB、tool calls 与 tool results 总数至少 20。只有在客户端取消且尚未收到任何上游事件时，才为相同 raw request fingerprint 标记一次；下一次相同请求在两分钟 TTL 内才应用 fallback。

因此当前实现不会处理 EOF、HTTP/2 错误、504、空事件后的失败或 reasoning-only 卡住。后续工作是扩展错误分类和可观测性，而不是重复实现三元组改写。

## 8. SLA 试验边界

1. 对 EOF、HTTP/2 stream error、deadline、504 和 reasoning-only stall 记录独立分类。
2. 指标至少包含 `first_actionable_ms`、reasoning-only 时长、reasoning 字节数、`[DONE]` 和最终完成原因。
3. 如试验 hedge，只在尚未产生 content/tool-call 的流式请求中启用，保留原请求并取先完成者。
4. hedge 需限制并发、缓冲、超时和额外 token 成本。
5. 所有非原生 profile 都必须 feature-gated；未完成账户侧验证前，不把 `question_refine` 作为普通用户请求的透明 fallback。

## 9. 会话关联的限制

有稳定 `session_id`、conversation ID 或 response 链路标识的入口可做显式映射。只有完整历史的 Chat Completions / Messages 调用默认保持无状态；按完整请求哈希、消息前缀或工具回合建立的 TTL 映射只能作为可关闭的启发式。

平台日志中的 Sessions 分组不足以证明其来源是调用方显式 ID、平台生成 ID 还是请求关系推导；在为平台入口实现会话映射前，仍需做脱敏抓包验证。

## 10. 数据处理约束

- 不提交 SQLite、WAL、HAR、原始重放输入、SSE 输出或凭据。
- 文档、fixture 和日志只保留字段结构、聚合计数、脱敏 fingerprint 和人工构造的最小内容。
- 原始采样仅用于短期本地排障，并在分析结束后按本地数据保留策略清理。
