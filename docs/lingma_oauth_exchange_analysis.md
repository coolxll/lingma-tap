# Lingma 鉴权与凭据交换机制分析报告 (修正版)

**版本：** v2.5.13  
**日期：** 2026-05-09  
**分析目标：** 探测 Lingma 如何通过 OAuth token 完成凭据交换及后续鉴权

---

## 1. 核心流程概览

Lingma 的鉴权体系采用了一种“令牌换取+本地签名”的二次封装机制。整个过程分为：**Token 解析** -> **凭据交换 (Auth APIs)** -> **本地持久化** -> **动态签名请求 (Business APIs)**。

1. **认证/心跳接口：** 使用 `Signature` Header (MD5)。
2. **业务接口 (如 agent_chat)：** 使用 `Authorization: Bearer COSY...` Header。

---

## 2. 关键阶段详解

### 2.1 OAuth 初始 Token 解析 (`parseAuthToken`)
当用户通过浏览器完成阿里云登录后，回调给本地 HTTP 服务一个加密的 `securityOauthToken`。
- **解析位置：** `cosy_auth.parseAuthToken` (@ `0x14120c4c0`)
- **逻辑：** 
  - 使用自定义的 `qoder` 变体 Base64 解码。
  - 解码后数据以 `\n` 分隔，包含三部分：`part0\npart1\nexpire_time`。
  - 其中的 `part0` 或 `part1` 作为后续交换的原始凭据。

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
  - **Prefix：** 固定字符串 `"cosy"`。
  - **分隔符：** 使用 `"&"` 进行 `strings.Join`。
  - **Date：** 标准 GMT 格式时间戳，必须与请求头中的 `Date` 字段绝对一致（如 `Mon, 02 Jan 2006 15:04:05 GMT`）。
  - **Secret 候选值：** 
    1. **候选A (推荐):** `"d2FyLCB3YXIgbmV2ZXIgY2hhbmdlcw=="` (Base64 解码为 `war, war never changes`)
    2. **候选B:** `"&Q3C3!N5mP5bbNcyryMY@KZtUFLRGbTe"`

### 2.3 本地凭据加密存储 (`saveAuthStatusToLocal`)
交换成功后，为了实现持久化登录，Lingma 会将凭据加密写入磁盘。
- **存储路径：** `~/.lingma/cache/user`
- **处理函数：** `cosy_auth.saveAuthStatusToLocal` (@ `0x141203900`) -> `cosy_user.SaveUserInfo` (@ `0x1405bbde0`)
- **加密机制：**
  1. 生成一个 16 位随机 UUID 作为 **本地签名密钥 (CosyKey)**。
  2. 使用 **RSA 公钥** 加密该 Key。
  3. 使用该 Key 通过 **AES** 加密完整的用户信息 JSON（包含正式 Token）。
- **作用：** 这里的 **CosyKey** 即为后续 `Bearer COSY` 签名中的关键参数。

---

## 3. 业务接口鉴权机制 (`Bearer COSY`)

适用于 `agent_chat` 等核心业务请求。
- **Header 格式：** `Authorization: Bearer COSY.<PayloadB64>.<SignatureMD5>`
- **生成位置：** `cosy_user.AuthToken` (@ `0x1405bb7a0`)

### 3.1 Payload (负载)
- 一个 Base64 编码的 JSON 字符串。
- 包含字段：`machine_id`, `aliyun_uid`, `prev_token` 等。

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

1. **凭据隔离：** 原始 OAuth 令牌仅在交换阶段有效，攻击者拿到 `user` 缓存文件也无法直接获得明文 Token（受 RSA/AES 保护）。
2. **请求防篡改：** `Bearer COSY` 签名机制引入了 Body、Path 和本地 Key 的绑定，有效地防止了请求重放和中间人篡改。
3. **设备绑定：** 签名计算中包含了 `machine_id`，使得凭据在不同物理设备间无法直接迁移使用。
