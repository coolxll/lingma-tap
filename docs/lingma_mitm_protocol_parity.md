# Lingma MITM 协议一致性与路由试点记录

- **日期：** 2026-07-12
- **状态：** 已确认请求元数据与原生客户端存在差异；不在未验证计费与语义前全局切换路由
- **适用项目：** `lingma-tap`（先行试点）、`cliproxyapi`（试点验证后同步）

## 1. 目的与证据边界

本文整理本次 MITM 抓包、当前实现审阅、受控 E2E 重放和原生客户端复现的共同结论，用于决定桥接层的请求 profile、fallback 和会话策略。

本文只记录字段结构、计数、状态和延迟；不记录 prompt、代码正文、认证信息、会话原值或原始 SSE 内容。原始抓包和重放数据只可在本地排障时短期保存，不提交到仓库。

证据分级：

| 标记 | 含义 |
|------|------|
| 已观察 | MITM 抓包或当前代码可直接确认 |
| 已复现 | 相同脱敏结构的受控重放可重复观察 |
| 假设 | 需要账户侧、更多样本或协议抓包验证，不能直接据此改生产语义 |

## 2. 结论摘要

1. 当前 bridge 已兼容核心传输、SSE 和 OpenAI/Anthropic 输出格式；主要差异在路由、`task_id`、模型元数据、会话关系和业务元数据。
2. `/sse/agent_chat_generation` 的一次请求是 Agent turn，不等同于裸 LLM completion。它可以自行进行长 reasoning、工具决策和输出；原生客户端还会在一轮结束后继续发起后续 turn。
3. `task_id` 已观察到与模型字段、usage 和实际响应路径相关，不能当作无影响的标签；但它与模型选择和业务场景共变，是否影响计费仍是**假设**，必须以账户侧数据验证。
4. 深工具历史下，`agent_chat` 的 HTTP/2 错误和 reasoning-only 卡住可由上游复现，原生客户端也会遇到。现有样本不支持把 `agent_common` 当作全局更优入口。
5. 当前代码已经具备“关闭 reasoning 的三元组 fallback”，但只会在特定大请求、无上游事件且客户端取消后，为下一次相同请求启用一次；它不会覆盖 EOF、504、已收到空事件或 reasoning-only 卡住。
6. 先在 `lingma-tap` 中做 profile 化、可观测性和受限实验；在语义、SLA 和计费都确认前，不同步改变 `cliproxyapi`。

## 3. 观察到的协议差异

### 3.1 主聊天 route profile

原生主聊天样本的主要 profile：

```text
agent_id=agent_common
task_id=common
source=1
chat_task=FREE_INPUT
session_type=assistant
task_definition_type=system
metadata.mode=agent
```

当前 `BuildLingmaBody` 的默认行为：

```text
task_id=question_refine
source=0
reasoning=true  -> agent_id=agent_chat,   model_config.source=system
reasoning=false -> agent_id=agent_common, model_config.source=""
```

对 `kmodel` 和 `mmodel`，当前实现始终使用 `agent_common` 和空 `source`。当前 body 不生成 `chat_task`、`session_type`、`task_definition_type` 或 `metadata`。

这说明当前 bridge 是“协议可用、元数据近似”的实现，而不是原生 IDE 主聊天请求的逐字段复刻。

### 3.2 会话、任务和请求 ID

原生主聊天中的关系：

```text
客户端会话 session_id
  -> 聊天任务 chat_record_id / request_set_id / business.id
    -> 多次上游 request_id（包括工具回合）
```

已观察到的原生语义：

- `request_id` 每次上游请求变化。
- `session_id` 在同一客户端会话中保持稳定。
- `chat_record_id`、`request_set_id` 和 `business.id` 可跨同一聊天任务的多个 turn 保持关联。

当前 bridge：

- 使用完整下游请求 JSON 的哈希作为 `session_id`；消息历史变化会得到新值。
- 每次 `chat_record_id` 等于新生成的 `request_id`。
- `request_set_id` 为空，`business.id` 每次重新生成。

因此当前实现不具备原生多轮会话语义。它不阻断单轮调用，但可能影响上游缓存、任务状态和多轮工具调用。

### 3.3 模型、参数和业务元数据

抓包中的 `gm51model` 主请求含有完整模型描述，例如模型显示名、格式、`max_input_tokens`、`price_factor` 和 `original_price_factor`。当前实现除 `key`、`is_reasoning` 外，模型字段大多为空值或零值。

原生主聊天同时设置 `max_tokens` 和 `max_new_tokens`；当前 bridge 只映射 `max_tokens`。原生 business 形态为 `product=ide`、`type=agent`、带真实 `begin_at` 和阶段变化；当前实现使用 `type=chat`、`begin_at=0` 和固定 `stage=start`。

这些差异尚未被证明是基础调用的阻断因素，但会影响协议保真度和后续兼容性。

### 3.4 Header 与响应

当前实现覆盖了核心鉴权、编码和请求路径。原生客户端还会发送 `Cosy-Bodyhash`、`Cosy-Bodylength`、`Cosy-Sigpath`、`X-Request-Id`、`X-Model-*` 及设备/组织相关 Header。

缺失 Header 尚未阻断基础调用。响应侧已兼容：

- `content`、`reasoning_content` 和 `tool_calls` delta。
- finish metadata、usage、cached tokens 和 reasoning tokens。
- `stop` 与 `tool_calls` 等完成状态。

SQLite 有两类相关记录，不能混为一谈：

- `gateway_logs` 在 payload logging 开启时保存 bridge 转换后的 Lingma 上游 body 与响应摘要，不能据此单独判断入站协议转换是否一致。
- MITM `proxy_records` 同时含请求/响应、解码 body 和 raw body 字段，能够用于协议比对，但属于敏感采样数据，不能导出、提交或作为常规日志依赖。

## 4. task_id 与 route profile

MITM 快照中观察到 `common`、`question_refine`、`memory_generation` 和 `agent_summary` 四种 `task_id`。它们在消息数量、tools、模型/价格字段和 usage 上有明显分组：

| task_id | 观察到的主要场景 | 模型/价格字段 |
|---------|------------------|---------------|
| `common` | 用户主 Agent、工具调用和后续工具回合 | 显式模型字段、非零 `price_factor`、`X-Model-*` |
| `question_refine` | 问题精炼/意图相关内部任务 | 模型 key 和价格字段可为空或零 |
| `memory_generation` | 后台 memory/eval | 模型 key 和价格字段可为空或零 |
| `agent_summary` | 对话摘要 | 模型 key 和价格字段可为空或零 |

最新 SQLite 快照中的 36 个原生 chat turn 进一步支持“不同 task profile”的观察，但不构成计费结论：

| task_id | turn 数 | 观察到的模型/价格组合 | `[DONE]` 标记 |
|---------|---------|------------------------|----------------|
| `common` | 18 | `gm51model` / 3，或 `qmodel_latest` / 0.1 | 18 |
| `memory_generation` | 12 | 空模型 key / 0 | 12 |
| `question_refine` | 4 | 空模型 key / 0 | 4 |
| `agent_summary` | 2 | 空模型 key / 0 | 2 |

同一快照的全部 36 个原生 chat turn 都使用 `agent_common`、未启用 reasoning；其中 `common` 请求规模明显更大。这个结果说明当前原生工作负载常使用非 reasoning 的 `agent_common` profile，**不**说明 `agent_common` 对 bridge 的 reasoning 请求或所有模型全局更稳定。

**已复现：** 在 3145/3202 的受控重放中，仅改变 `task_id` 即可改变部分请求的实际结果；同一 Agent/模型配置不能视为在不同 `task_id` 下等价。

**假设：** `task_id`、模型字段和 `price_factor` 可能共同参与计费或额度归因。当前没有账户侧对照，不能据此断言具体计费规则，也不能静默把普通用户请求伪装为 `question_refine`。

当前决策：将 `task_id` 视为 route profile 的组成部分。任何非原生主聊天 profile 都必须 feature-gated、可观测，并标记为降级实验。

### 4.1 关键推论的重新论证

| 推论 | 支持证据 | 反证或混杂因素 | 当前决策 |
|------|----------|----------------|----------|
| `task_id` 是实际路由输入 | 原生抓包中请求形态分组；3145/3202 改变 `task_id` 后结果变化 | `task_id` 与模型、消息规模和内部任务共同变化 | 作为 route profile 字段处理，不当作纯标签 |
| `task_id` 会改变计费 | `common` 与内部任务的模型/价格字段不同 | 没有账户侧对照；模型 key 本身已经变化 | 不作计费结论，先做最小对照实验 |
| `agent_common` 全局更稳定 | 当前原生 36 turn 均为 `agent_common` 且有 `[DONE]` | 该样本均为非 reasoning；3145/3202 在 `common` 下的 `agent_common` 仍失败 | 不全局切换；保留 profile 化实验 |
| 失败来自上游而非 bridge | 原生客户端可出现同类 reasoning 卡住；不同模型可处理 3145 结构 | bridge 的路由、session 和元数据仍与原生不同 | 视为上游风险已证实，bridge 兼容差异继续排查 |

这四项中，只有“`task_id` 进入实际请求路由”和“上游存在独立的不稳定分支”具备足以影响实现的证据；计费规则和全局最优 Agent 入口均未被证明。

## 5. Agent 路由与 E2E 结果

### 5.1 3145：深工具历史后的 HTTP/2 错误

3145 与成功的 3143 前 32 条消息和 tools 结构相同，只多出一轮 assistant tool-call 与两个 tool result。清空工具参数和结果正文、移除顶层 tools、随机化请求/会话/业务 ID 都无法解除错误；移除整个新增工具轮次后可正常完成。

这表明触发因素更接近“深工具调用历史 + 再增加完整工具轮次 + 特定上游 profile”的组合，而不是具体工具文本、参数或 ID。约 18 个 tool results 附近只是观察到风险升高，不是可编码的绝对阈值。

| profile | task_id | 结果 |
|---------|---------|------|
| `agent_chat + reasoning + system` | `question_refine` | 约 67 秒后 HTTP/2 `INTERNAL_ERROR` |
| 仅改为 `agent_common` | `question_refine` | 部分输出后约 35 秒 `unexpected EOF` |
| `agent_common + reasoning=false + source=""` | `question_refine` | 约 12.5 秒正常完成 |
| 四种 agent/reasoning/source 组合 | `common` | 均为 HTTP/2 错误，约 64-82 秒 |

结论：完整三元组在该 `question_refine` 样本中优于原路径，但在 `common` 下没有成功。不能把它表述为任意请求的确定性修复。

### 5.2 3202：reasoning-only 卡住

3202 出现长时间自我校验式 reasoning，没有 content 或 tool-call；降低 `max_tokens` 未可靠结束该 turn。替换单条 user、assistant tool-call 或 tool result 不足以解除问题，说明它更像完整对话状态组合触发。

| profile | task_id | 结果 |
|---------|---------|------|
| `agent_chat + reasoning + system` | `question_refine` | 90 秒 reasoning-only，无完成 |
| 仅改为 `agent_common` | `question_refine` | 约 2 秒正常发出 tool call |
| 完整三元组 | `question_refine` | 约 2.2 秒正常完成 |
| `agent_chat + reasoning + system` | `common` | 约 88 秒完成，其中约 84.5 秒为 reasoning-only |
| `agent_common` 的三个变体 | `common` | reasoning-only 超时或空流 EOF |

结论：`max_tokens` 不是可靠的上游 reasoning budget 控制；`agent_common` 也不是对所有 `task_id` 更稳定的入口。

### 5.3 原生客户端与跨模型对照

将同一问题序列在原生客户端重走时，也观察到长 reasoning 和超时；原生客户端的多个后续 Agent turn 仍来自同一上游 Agent 端点。这为“问题至少部分位于上游 Agent 行为”提供强证据，但不能排除 bridge 的元数据差异带来的额外影响。

将 3145 的结构化请求重放到第三方 OpenAI 兼容模型服务时，至少有一个不同模型正常完成。这说明请求内容本身并非必然不可处理；该结果是单次对照，不代表第三方模型、兼容中继或任何聚合平台的长期可用性。

## 6. 当前恢复行为

`internal/bridge/thinking_fallback.go` 已实现三元组改写：

```text
agent_id:      agent_chat  -> agent_common
is_reasoning:  true        -> false
source:        system      -> ""
```

共同上游层现已加入顺序恢复：

- 尚未向调用方提交可见输出时，HTTP 408/425/429/5xx、EOF、HTTP/2/reset/timeout 和可恢复 SSE error 默认最多尝试 3 次。
- 每个 retry attempt 刷新请求级 ID 并保持 `session_id`，避免重放完全相同的上游请求身份。
- 对 `gm51model` 的 reasoning 请求，body 至少 128 KiB 且工具历史计数至少 20 时，reasoning-only delta 最多暂存 45 秒；没有 content/tool-call/完成信号即在同一入站请求内切换三元组 recovery。
- 暂存最多 2 MiB；一旦已提交 content/tool-call，后续失败不自动重试。
- 原有 fingerprint + TTL fallback 保留，用于客户端取消或同请求恢复耗尽后的下一次完全相同调用。

这覆盖了此前的首输出前 EOF、HTTP/2 错误、5xx、空事件失败和满足风险 profile 的 reasoning-only stall。它不是并发 hedge，也不会在已经输出后重放；`common` 与 `question_refine` 的语义/计费验证仍未完成，因此没有全局改变 `task_id`。

GatewayLog 同步记录 attempt 数、是否 recovery、上游错误分类、首个可行动输出延迟、暂存 reasoning 字节数及 requested/effective profile。2026-07-12 的约 140 KiB、10 轮合成工具历史实网测试连续 4 次单 attempt 完成，首个可行动输出为 1.62-3.57 秒；这只证明人工 workload，不代表生产长上下文分布已完全覆盖。

## 7. 会话策略

按入口能力选择会话策略，不使用单一全局方案：

| 入口能力 | 策略 | 结论 |
|----------|------|------|
| 显式 `session_id` / conversation ID | 带调用方命名空间的直接映射 | 首选 |
| Responses 等具稳定 response/conversation 关联的接口 | 建立显式链路映射 | 可实施，需按接口验证 |
| 平台侧 session 分组 | 先抓包确认 ID 来源、稳定性和租户隔离 | 尚未确认 |
| 只有完整历史的 Chat Completions / Messages | 默认无状态 | 不伪造原生会话语义 |

对 Claude Code、Hermes 等没有稳定标识的调用，消息前缀或请求哈希只能作为可关闭的 TTL 启发式；它会受并发、截断和历史修改影响，不能当作真实 session。

## 8. 试点计划

### Phase 1：profile 与可观测性

1. 显式建模 `task_id`、AgentId、`is_reasoning`、model source、business type 和降级状态。
2. 记录 requested/effective profile、fallback reason、`first_actionable_ms`、reasoning-only 时长、EOF/HTTP2/504 分类和 `[DONE]` 状态。
3. 日志只保留脱敏 fingerprint 与计数，不保存 prompt、tool result 或原始 SSE。

### Phase 2：计费与语义验证

1. 使用最小无 tools 请求分别验证合法 `common` 与 `question_refine` profile。
2. 对照账户侧用量或账单，确认 `task_id`、模型字段和 `price_factor` 的影响。
3. 比较模型能力、reasoning、usage、完成率和输出截断。
4. 在结果明确前，禁止把 `question_refine` 当作普通聊天的透明 fallback。

### Phase 3：SLA 实验

1. 扩展 fallback classifier，覆盖 EOF、HTTP/2 stream error、deadline、504 和 reasoning-only stall。
2. 仅对“尚未产生 content/tool-call”的流式请求试验 hedge；保留原请求，取先完成者。
3. hedge 必须有最大并发、缓冲上限、超时和额外 token 成本指标。
4. 不按“tool result 数量达到某阈值”做全局预路由；仅将其作为 profile 风险信号。

### Phase 4：会话实验与同步

1. 为带稳定 ID 的入口增加命名空间隔离的直接映射。
2. 对平台侧 session 分组的来源做脱敏抓包验证。
3. 无稳定 ID 的入口保持无状态兼容。
4. 只有在 `lingma-tap` 的成功率、延迟、语义、计费和额外成本均无明显回归后，才同步到 `cliproxyapi`。

## 9. 验收指标

- 各 profile 的成功率、4xx/5xx、EOF 和 HTTP/2 错误分布。
- 首个 content/tool-call 延迟、总延迟、reasoning-only 时长与字节数。
- `stop` / `tool_calls` 完成率，以及多轮工具调用完成率。
- input/output/cached/reasoning token 的完整性与截断率。
- hedge 启动率、胜率、取消率和额外 token 成本。
- `common` 与 `question_refine` 的账户侧额度/费用差异。

## 10. 非目标与安全约束

当前不实现 IDE 专属的 `business/finish`、embedding、rerank、tracking、workspace、patches 或 memory 业务语义。

- 不提交 SQLite 抓包数据库、WAL、HAR、原始请求/响应或凭据。
- 不在 fixture、文档和日志中保留 prompt、代码正文、Authorization、CosyKey、真实 session ID 或 tool result。
- 原始报文如需临时排障，必须在本地受控保存并在分析结束后清理。
