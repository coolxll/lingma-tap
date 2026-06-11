# Lingma 鉴权与凭据交换机制分析报告 (修正版)

**版本：** v2.6.0
**日期：** 2026-06-08
**分析目标：** 探测 Lingma 如何通过 OAuth token 完成凭据交换及后续鉴权
**修正记录 (v2.6.0):** 基于 `lingma-analysis` 代码交叉验证，修正 Bearer Payload 字段、Token 回调描述、存储机制区分、设备绑定与防篡改结论。

---

## 1. 核心流程概览

Lingma 的鉴权体系采用了一种“令牌换取+本地签名”的二次封装机制。整个过程分为：**Token 解析** -> **凭据交换 (Auth APIs)** -> **本地持久化** -> **动态签名请求 (Business APIs)**。

1. **认证/心跳接口：** 使用 `Signature` Header (MD5)。
2. **业务接口 (如 agent_chat)：** 使用 `Authorization: Bearer COSY...` Header。

---

## 2. 关键阶段详解

### 2.1 OAuth 初始 Token 解析 (`parseAuthToken`)
当用户通过浏览器完成阿里云登录后，回调给本地 HTTP 服务（`localhost:37510/auth/callback`）携带两个 Encode=1 编码的参数。
- **解析位置：** `cosy_auth.parseAuthToken` (@ `0x14120c4c0`) / `parseAuthInfo` (@ `0x141a21660`)
- **回调 URL 格式：**
  ```
  /auth/callback?state=2-{nonce}&auth=<Encode1>&token=<Encode1>
  ```
- **逻辑：**
  - `state` 参数中 `2-` 前缀触发 V2 回调模式（无前缀为 V1 旧模式）。
  - 使用自定义的 Encode=1 变体 Base64 解码（自定义字母表 + 三块反转）。
  - **`auth` 参数**：解码后以 `\n` 分隔，包含三部分：`uid\naid\nname`。
  - **`token` 参数**：解码后以 `\n` 分隔，包含三部分：`security_oauth_token\nrefresh_token\nexpire_time`。
  - Token 格式：`pt-{22 chars}`（securityOauthToken）、`rt-{22 chars}`（refreshToken）。

### 2.2 凭据交换与认证接口签名 (`Auth APIs`)
适用于接口：`/api/v3/user/grantAuthInfos`, `/api/v3/user/status`, `/api/v1/heartbeat` 等。

#### 2.2.1 `Encode=1` 请求体封装
当 URL 携带 `Encode=1` 时，原始 JSON 请求体会被二次封装：
- **格式：** `{"payload": "<EncodedString>", "encodeVersion": "1"}`
- **EncodedString：** 原始 JSON 经过 `qoder` (自定义 Base64 变体) 编码后的字符串。

#### 2.2.2 `Signature` Header 计算
经过对 `cosy_remote.addBigModelSignatureHeaders` 的汇编级追踪，签名的精准生成逻辑如下：
- **核心公式：** `MD5("cosy" + "&" + Secret + "&" + Date)`
- **Header 联动：** 同时会在 HTTP 请求头中增加 `Appcode: cosy` 和对应的 `Date` 头。
- **参数详情：**
  - **完整前缀字符串：** `"cosy&"` 与 Secret 和 Date 用 `&` 拼接后整体做 MD5，即 `MD5("cosy&{Secret}&{Date}")`。
  - **Date：** 标准 RFC 1123 GMT 格式时间戳，必须与请求头中的 `Date` 字段绝对一致（如 `Mon, 02 Jan 2006 15:04:05 GMT`）。
  - **Secret 候选值：**
    1. **候选A (推荐):** `"d2FyLCB3YXIgbmV2ZXIgY2hhbmdlcw=="` (Base64 解码为 `war, war never changes`)
    2. **候选B:** `"&Q3C3!N5mP5bbNcyryMY@KZtUFLRGbTe"`

### 2.3 本地凭据加密存储 (`saveAuthStatusToLocal`)
交换成功后，为了实现持久化登录，Lingma 会将凭据加密写入磁盘。存在**两套独立的存储机制**：

#### 2.3.1 Lingma 官方客户端缓存
- **存储路径：** `~/.lingma/cache/user`
- **处理函数：** `cosy_auth.saveAuthStatusToLocal` (@ `0x141203900`) -> `cosy_user.SaveUserInfo` (@ `0x1405bbde0`)
- **加密机制：**
  1. AES 密钥 = `machine_id` 的前 16 个字符（来自 `~/.lingma/cache/id`）。
  2. 使用 AES-128-CBC 加密完整的用户信息 JSON，key 和 IV 相同。
  3. Base64 编码后写入 `user` 文件。

#### 2.3.2 独立工具生成（`portable_config.json`）
- **存储路径：** `~/.lingma/portable_config.json`
- **生成函数：** `generate_cosy_credentials`（本地完成，无需服务器交互）
- **加密机制：**
  1. 生成随机 16 字节 AES key（`uuid.uuid4().hex[:16]`，每次不同）。
  2. 使用 **RSA-1024 公钥**（IDA 地址 `0x1425bd8e8` 硬编码）PKCS1v15 加密该 key → Base64 → `cosy_key`。
  3. 使用该 key 通过 **AES-128-CBC** 加密用户信息 JSON（key=IV=同一个随机 key，PKCS7 填充）→ Base64 → `encrypt_user_info`。
- **作用：** `cosy_key` 和 `encrypt_user_info` 即为后续 `Bearer COSY` 签名中的关键参数。

---

## 3. 业务接口鉴权机制 (`Bearer COSY`)

适用于 `agent_chat` 等核心业务请求。
- **Header 格式：** `Authorization: Bearer COSY.<PayloadB64>.<SignatureMD5>`
- **生成位置：** `cosy_user.AuthToken` (@ `0x1405bb7a0`)

### 3.1 Payload (负载)
- 一个 Base64 编码的 JSON 字符串。
- 包含字段：
  ```json
  {
    "cosyVersion": "2.11.2",
    "ideVersion": "",
    "info": "<encrypt_user_info>",
    "requestId": "<uuid4>",
    "version": "v1"
  }
  ```
- 其中 `info` 是 2.3.2 节 AES 加密后的用户信息 Base64 字符串。
- `machine_id` 不在 Payload 中，而是通过 HTTP Header `Cosy-MachineId` 传递。

### 3.2 Signature (签名) 计算
经确认，正确的换行符拼接顺序如下（该顺序已被验证可行）：
- **公式：** `PayloadB64 + \n + CosyKey + \n + cosyDate + \n + encodedBody + \n + pathWithoutAlgo`
- **字段说明：**
  - `PayloadB64`: 负载的 Base64 字符串。
  - `CosyKey`: 2.3 节生成的本地随机密钥。
  - `cosyDate`: 与 Header 中 `Cosy-Date` 一致的时间字符串。
  - `encodedBody`: 如果是 POST，则为完整的请求体字符串（含 `Encode=1` 的封装）。
  - `pathWithoutAlgo`: 去除 `/algo` 前缀的请求路径。

---

## 4. 结论与安全特性

1. **凭据隔离：** 原始 OAuth 令牌仅在交换阶段有效。`~/.lingma/cache/user` 缓存文件使用 AES 加密（key = `machine_id[:16]`），拿到文件 + machine_id 即可解出明文 Token。`portable_config.json` 中的 `cosy_key` 使用 RSA 加密 AES key，但 RSA 公钥硬编码在二进制中，任何人可以生成自己的 cosy_key/encrypt_user_info 对。
2. **请求完整性：** `Bearer COSY` 签名机制引入了 Body、Path 和本地 Key 的绑定，提供了一定程度的请求完整性保护。但签名中**不含 nonce 或请求序列号**，Date 精度仅到秒，同一秒内可重放。防篡改能力有限。
3. **无设备绑定：** Bearer 签名前缀为 `payload_b64\n{cosy_key}\n{date}\n{body}\n{path}`，**不包含 `machine_id`**。`machine_id` 仅出现在 HTTP Header `Cosy-MachineId` 中，不在签名计算范围内。因此凭据可跨设备迁移使用——复制 `portable_config.json` 即可。
