# 账号级采集凭据（R-collector-001）

解决一件事：**中转站的余额/用量怎么读到**。

实测过的现实：15 家启用渠道里，12 家站点根本没有余额接口（404），3 家要站点面板的访问令牌
（用 sk- Key 只能拿到 401/403）。也就是说光靠 API Key 对大多数站点无解 —— 只能从"账号"这一层入手。
本功能给两条路，**两条路都在同一套会话/读取机制上跑**：

| 路 | 谁登录 | 适合什么 | 状态 |
| --- | --- | --- | --- |
| **交互登录** | 人（在 octopus 托管的登录页里输账号、验证码） | 站点有图形验证码 / 短信 / 扫码；不想把密码交给软件 | 已实现并活体验证 |
| **采集包** | 软件（用你填的账号密码，或已有会话） | 你已把站点登录/查账接口逆向清楚，想长期自动采集 | 已实现并活体验证 |

---

## 1. 交互登录：把站点登录页反代成临时地址

```
POST /api/v1/collector/sources            # 建凭据：kind=login、site=站点根地址、pack=读取步骤
POST /api/v1/collector/sources/:id/login  # 返回 login_url，例如 /api/v1/collector/login/3/
      ↑ 在浏览器打开这个地址，就是那个站点的登录页（图片/表单都在 octopus 上）
POST /api/v1/collector/sources/:id/login/finish   # 登录完成后点"完成"：保存会话 + 立刻试读一次
```

- 登录页由 octopus 服务端反代：`Set-Cookie` 被**服务端捕获**，不依赖你手工复制任何东西。
- 页面里的地址会被改写到代理前缀（`href/src/action` 的绝对地址与根相对地址），保证你一直停在代理内。
- 响应里的 `X-Frame-Options` / CSP 会被去掉，所以可以嵌在面板里；`Domain` 去掉、`Secure` 去掉、
  `SameSite=None` 降级为 `Lax`，这样在 http 面板下 Cookie 才生效。
- **我们自己的管理 Cookie（`auth`）永不转发给上游** —— 有专门的用例钉住这一点（实测踩过：
  只在"过滤后还剩别的 Cookie"时才覆盖头，导致只有一个 auth Cookie 时被整包转发出去）。
- 上游重定向到别的站点时一律拉回自己的前缀（不做开放代理）。

## 2. 采集包：把你逆向出来的"包"装进软件

包是一份 JSON，定义在 `internal/collector/pack.go`。**装包即得监控**：包被校验、加密落库，
之后由余额扫描周期自动执行。

```json
{
  "name": "某中转站",
  "units": "usd",
  "hosts": ["api.example.com"],
  "login": {
    "mode": "form",
    "url": "https://api.example.com/api/user/login",
    "body_type": "json",
    "body": "{\"username\":\"{username}\",\"password\":\"{password}\"}",
    "extract": [{"field": "token", "from": "json", "path": "data.token"}]
  },
  "read": [
    {
      "name": "读余额",
      "url": "https://api.example.com/api/user/self",
      "headers": {"Authorization": "Bearer {token}"},
      "extract": [
        {"field": "balance",  "from": "json", "path": "data.quota"},
        {"field": "used",     "from": "json", "path": "data.used_quota"},
        {"field": "currency", "from": "json", "path": "data.unit"},
        {"field": "username", "from": "json", "path": "data.username"}
      ]
    }
  ]
}
```

### 字段速查

| 字段 | 说明 |
| --- | --- |
| `units` | 读数单位：`usd`（默认）或 `points`（new-api 的点）。**必须写对**：写成 usd 的 32.53 不会再被折算，写 points 的 16265000 会按 `balance_points_per_unit` 折算 |
| `hosts` | **必填**：允许访问的主机。请求打到未声明主机、或重定向到未声明主机，一律拒绝 |
| `login.mode` | `form`（软件登录，默认）或 `interactive`（跳过登录，用交互登录捕获的会话） |
| `login.body_type` | `json` / `form` |
| `login.body` | 正文模板，可用 `{username}` `{password}` `{captcha}` `{otp}` `{site}` |
| `login.extract` | 从登录响应里捞变量（如 `token`），后续步骤可用 `{token}` 引用 |
| `read[].method` | 只允许 `GET` / `POST` |
| `read[].extract[].from` | `json`（点号路径，支持数组下标）/ `regex`（可带 `group`）/ `header` / `status` |
| 账字段 | `balance`（剩余）、`used`（已用）、`quota`（总额度，缺 balance 时用 `quota − used` 推）、`currency`、`username`、`note`；其它字段名一律作为变量留给后续步骤 |

### 装载门禁（fail closed）

- 没有 `hosts`、请求打到未声明主机、非 http/https、未知占位符、非法正则、非法字段名、未知顶层字段、
  方法越权、末了取不到 `balance`/`quota` —— 任一命中即**拒绝装载**（不是"猜一半"装上）。
- 包与凭据都用**分用途的 AAD 加密落库**（`collector:pack` / `collector:credential` / `collector:session`），
  接口出参永不回显密码与会话内容，只给 `has_credentials` / `has_session`。
- 采集器出网走 `http.ProxyFromEnvironment`，因此**在插件/内核出口体系里可以按需要绑节点**；
  读失败时记原因码（`unauthorized` / `no_endpoint` / `unreachable` / `unparsable`）并进面板的原因归类。

## 3. 接口一览（全部管理面鉴权）

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/v1/collector/sources` | 列表（含采集包原文、会话状态、最近步骤轨迹） |
| POST | `/api/v1/collector/sources` | 新建（`name`/`kind`/`site`/`channel_id`/`pack`/账号密码） |
| PUT | `/api/v1/collector/sources/:id` | 改（留空即不改：改开关不必重发整份包与密码） |
| DELETE | `/api/v1/collector/sources/:id` | 删除（行与会话一起消失） |
| POST | `/api/v1/collector/sources/:id/run` | 立即采集一次 |
| POST | `/api/v1/collector/sources/:id/login` | 开始交互登录，返回临时地址 |
| POST | `/api/v1/collector/sources/:id/login/finish` | 完成登录（保存会话 + 试读） |
| DELETE | `/api/v1/collector/sources/:id/session` | 清除会话（重新登录） |
| GET/POST | `/api/v1/collector/login/:id/*path` | 登录页反向代理本体 |

绑定 `channel_id` 且开启"自动刷新"的采集凭据，会在余额扫描里**优先于通用协议探测**执行
（用户明确指定的读法更权威），读到的数按 `units` 进余额管线，低于阈值同样触发告警与归零停表。

## 4. 诚实的边界

- 交互登录是**文本级**地址改写：用 JS 动态拼出的地址、写在 CSS/JS 里的相对路径改不到。这类站点请改用"包"直接打接口。
- 若站点的会话只存在浏览器 `localStorage`（不放 Cookie），交互登录拿不到它 —— 这时只能靠"包"把令牌提取出来。
- 采集器是在**服务端**发请求：面板必须是你能访问 octopus 管理面的那一台（本机或内网），不要为了登录页把管理面暴露到公网。
- 密码只有在你选择"包模式 + 填账号密码"时才需要交给软件；只想给会话就选交互登录，凭据不落库、只落会话密文。
- "读得到余额"仍取决于站点是否给出可解析的字段；站点改版会让包失效，此时 `run` 会报
  `unparsable` 并点名缺失的字段路径（而不是含糊说"凭据不对"）。

## 内置站点模板（R-site-template-002）

面板「扩展 → 采集凭据 → 从站点类型开始」把"输入账号密码登录站点看余额"降成 **选类型 + 填网址**：
后端按该类型的惯例接口生成一份采集包填进表单，人只需要核对。接口：

```
GET  /api/v1/collector/templates          列出 4 类 × 2 形态
POST /api/v1/collector/templates/render   {kind, base_url, mode} → {pack, units}
```

| 站点类型 | 定位 | 登录 / 查账端点 | 额度单位 |
| --- | --- | --- | --- |
| `new-api` | one-api 商业 fork，当前主流 | `POST /api/user/login` → `GET /api/user/self`（`data.quota` / `data.used_quota`） | 点 |
| `one-api` | songquanpeng/one-api 初代网关，存量最多 | 同上（同源接口） | 点 |
| `sub2api` | 订阅账号（Claude Plus / ChatGPT Plus）逆向转 API | `POST /api/auth/login` → `GET /api/user/info` | 美元 |
| `litellm` | Python 网关，海外/企业向 | `POST /user/login` → `GET /user/info`（`user_info.max_budget` / `spend`） | 美元 |

每条模板都有两种登录形态，区别是真实存在的：

- **form（账号密码直连）**：包自己 POST 登录拿会话；站点挂了图形验证码 / Turnstile 时**必然失败**。
- **interactive（反代登录页）**：octopus 把站点登录页反代成临时地址，人在页面里输验证码，服务端捕获会话；
  之后仍按模板的查账端点读余额（包里的 `login.mode = interactive`，不带登录接口地址）。

四条硬口径：

1. **生成即过装载门禁**：`BuildTemplate` 出来的包先跑一遍 `Validate()`，不合格就不发给前端（模板自己写错时
   用户会白填一次表单、而且在装载处看不出是谁的错）；
2. **主机白名单来自用户填的网址**，模板绝不预置域名 —— 预置等于绕过 fail closed 的主机约束；
3. **单位写死在模板里**（new-api/one-api 是点、sub2api/litellm 是美元），落库时按 `balance_points_per_unit` 折算；
   单位错会让余额差 50 万倍（实测踩过）；
4. 模板是**起点不是保证**：sub2api 与 litellm 各家前端改得多，登录端点/字段路径可能要在生成结果上微调。

判据：`internal/collector/templates_test.go`（每类 × 两形态都必须过门禁、hosts 只含所填主机、所有请求地址不越界、
interactive 版本不带 API 登录步骤、单位表、坏输入被拒、网址补全 scheme）+ 活体 `%TEMP%\octopus-dstest\verify_site_templates.py`。

## 真实站点实测（2026-09-20）与由此产生的三处修正

用户给了 8 家真实中转站账号，全部**经本地出口节点**登录并读账（不让真实 IP 进站点风控）。

| 站点 | 判定 | 实测结果 |
| --- | --- | --- |
| api.uu6.top / pipixia1.online | 真 new-api | 登录返回 `data.access_token`；**只带 Cookie 读不到**，必须 `Authorization: Bearer`；`data.quota=6300000` ⇒ 12.60 USD |
| okai.la / cochacode.com / aiaaa.cc / sub.tohoqing.com | 同一套 SPA 平台 | `POST /api/v1/auth/login {email,password}` → `data.access_token`；`GET /api/v1/user/profile` 的 `data.balance` 就是美元余额（0.82 / 25.07 / 30.80 / 8.31） |
| api.53hk.cn / apikey.fun | 同平台但挂了验证码 | geetest / 腾讯验证码直接拒（`captcha verification failed`）——form 模式如实失败，不假成功；要读只能走反代登录 |

三处修正（都有单测钉住）：

1. **new-api 余额口径**：`data.quota` 是**剩余额度**本身，不是总额度。原模板按「总额度 − 已用」推导，实测算出 `-187900000` 点（≈ **-375.8 USD**）的负数余额。现改为直接以 `balance` 收 `data.quota`，并**不再声明 quota 规则**（否则推导分支被激活）。
2. **AI Gateway 站点族**（新增 `ai-gateway` 模板）：六家实测同一套产品，路径族完全一致（前端 bundle 里 `/auth/login`、`/auth/me`、`/oauth/*` 逐字相同），真接口是 `/api/v1/auth/login`（Go 校验 `LoginRequest.Email` 点名的字段）。
3. **交互登录也能拿到令牌**：SPA 的会话不是 Cookie 而是响应体里的 `access_token`。反代登录页时若只捕获 Cookie，用户明明登录成功、后续采集却一律 401。现在 `LoginProxy` 会把流经代理的 JSON 响应里的 `access_token`/`token` 捞进会话变量，供采集步骤用 `{token}` 引用（因此 `token` 必须无条件在占位符白名单里）。

## 采集出口节点（不让真实 IP 进站点）

`credential_sources.proxy_node_id`：0 = 直连；非 0 = 该凭据的**采集与反代登录**都从这台出口出去
（`collector.RunVia` / `NewLoginProxyVia`，传输层固定代理，主机白名单与重定向约束照旧生效）。

- 面板「扩展 → 采集凭据」里每行都有出口下拉，新建表单也有；不选就是从本机真实 IP 发出，选项里写明了这一点。
- **fail closed**：绑定的节点不存在 / 未启用 / 内核没给它分配端口，采集直接报错
  （`采集出口不可用：代理节点不存在（id=…）：请重新选择节点`），**绝不静默改走直连**。
- 实测：8 家站点分别绑不同出口全部成功；绑 `id=999999` 立即 400 拒绝；改绑后同一条凭据再采集正常。
- 踩过的两个坑（都是"保存路径漏字段"这一类）：`ProxyNodeID` 原本写在 `if in.Site != ""` 分支里 ⇒ 只改出口被静默丢弃；更新路径的 `Select(...)` 列清单漏 `proxy_node_id` ⇒ 返回 200 但库里没变。现在输入字段用 `*int` 区分「没给」与「显式改直连」，两处都补了回归用例。
