# Sub2API Round 1 独立代码审查与 Round 2 规划

审查日期：2026-10-21。审查基线：`6b837e4`；范围：`81769d8..6b837e4`，包含 `b2d4a0c`、`6b837e4` 两个提交，9 个文件，903 行新增、278 行删除。审查开始时工作区干净。

## 结论与评分

**48/100，建议退回完善；暂不按“订阅反代网关已完成”验收发布。** 当前交付是账号配置的新增、查询、删除、探测，以及可选创建渠道记录。缺少可自动参与选路的模型与授权闭环；账号上的协议、认证类型等配置未形成执行契约。发现 8 项 HIGH、2 项 MEDIUM；未确认 BLOCKER。HIGH 按 code-review 技能口径应在发布前修复。

| 维度 | 得分 | 依据 |
| --- | ---: | --- |
| 正确性和业务闭环 | 11/30 | 渠道缺模型和授权，配置语义未贯通，禁用位失效 |
| 安全和防御 | 10/20 | API key 复用 AES-GCM 和独立 AAD；探测绕过绑定出口，敏感头明文存储 |
| 架构和维护性 | 12/20 | model/op/handler 分层与既有 ChannelCreate 复用合理；双实体一致性不足 |
| 产品体验和配置可解释性 | 8/15 | 基本管理界面可用；探测误报、配置能力过度表述、缺修复入口 |
| 测试和可验证性 | 7/15 | 全量现有测试通过；新增测试只覆盖不物化渠道的创建、查询、删除 |
| 合计 | **48/100** | 分数是本次交付质量判断，不代表原项目整体质量 |

已做对的部分：新表已登记 AutoMigrate 和 fresh-install 测试；新增管理 API 挂在 ServerAdmin 并使用 Auth；主要 api_key 通过 secret.SealWith 加密，Credentials 不进入普通 JSON；没有重写既有 relay 状态机。复杂度不高，主要问题在执行契约缺失和错误被吞掉。

## 审查发现

### F1 — HIGH：物化渠道没有模型、授权与分组成员，无法自动参与转发

位置：`internal/op/sub2api.go:141`（ChannelDetail 构造）。只提交 ChannelConfig 和 Keys，没有 Models、Grants。ChannelCreate 的自动分组依赖模型和授权，空模型直接返回。因此“自动物化注册为系统渠道（同步接入路由分发与故障转移）”实际只完成渠道记录创建。

复现：启用 sync_channel、指定 responses 后创建账号，channel_id 非零，但 channel_models、channel_grants、group_items 都为零。用户仍须在渠道页面手工补模型和授权；界面没有说明这个前提。

修改方向：明确选择模型的流程；支持手工模型列表，模型发现作为可选辅助；物化时写 Models 和带协议位的 Grants，再由既有自动分组逻辑建成员。未完成时必须显示“待配置”，不能显示可路由成功。

### F2 — HIGH：协议与认证选项没有接入执行，平台列表不能证明九个平台能力已实现

位置：`internal/op/sub2api.go:91`、`:118`、`:204`；`web/src/components/modules/account/index.tsx:27`。AccountType、APIProtocol、AccountMode、RateLimit 落库后没有影响本次新增的转发物化逻辑。Probe 统一把唯一的 api_key 拼成 Bearer。协议选择也没有变为 ChannelGrant.Protocols。OAuth 没有此新账号实体的授权、refresh token、过期刷新或 provider adapter；Setup Token 没有专门处理。既有 OfficialAccount 的 OAuth 实现仍属于另一个实体，不等于新 Sub2APIAccount 已接通。

具体影响：选择 Anthropic 或 Responses 只改变账号卡片文字，无法据此获得对应出站能力；直接填官方站点并选择 OAuth 不能据此把订阅转换成 API。固定 api_key 可以代表外部已部署网关的访问凭据，但这种模式不应承诺实现上游订阅会话管理。

修改方向：Round 2 首先确定为“连接外部 Sub2API 网关”。本地处理网关地址、网关 key、能力和出口；外部网关负责订阅 OAuth 和刷新。对尚未接线的模式禁选并说明能力，限流要明确 RPM 或并发，不能共用一个含糊字段。只有决定本地直接管理订阅时，才另开适配器项目。

### F3 — HIGH：跨账号/渠道写入不一致，创建和删除均会静默失败

位置：`internal/op/sub2api.go:106`、`:111`、`:158`、`:176`。账号先提交，渠道创建错误被 `_ =` 丢弃；channel_id 更新也不检查 Error/RowsAffected；删除渠道失败后仍删除账号。ChannelCreate 本身已经提交后还可能遇到缓存刷新错误，不能简单把任意返回错误当作数据库一定没写入。

复现一：同平台同名创建两次。账号允许同名，渠道 Name 有唯一约束，第二个账号成功返回但 channel_id=0。复现二：注入渠道删除错误，账号删除 API 仍成功，账号消失，启用渠道保留。

修改方向：复用/抽出接收 tx 的渠道写库原语，使账号、渠道、子表、关联写回共享事务；缓存仅在提交后刷新并单独处理失败。或者明确采用持久化同步状态和可重试补偿的设计。渠道命名增加稳定账号标识；增加关联唯一约束/所有权检查，成功返回必须可验证。删除失败必须保留可重试状态，禁止无提示遗留可用渠道。不要直接在外层 Transaction 中调用仍用全局 DB 并提前更新缓存的 ChannelCreate，容易破坏事务与缓存边界。

### F4 — HIGH：显式 enabled:false 被 GORM default:true 改成 true

位置：`internal/model/sub2api.go:43`、`internal/op/sub2api.go:98`、`:106`。Enabled 是带 default:true 的 bool；Create 时 false 作为零值被默认值替换。物化渠道使用的是已被替换后的 account.Enabled。

复现：创建请求 enabled:false，返回 enabled:true；同步创建的渠道也会按 true 创建。前端目前固定传 true，不会触发这一问题，但管理 API 已公开支持该参数。

修改方向：请求 DTO 用 *bool 区分缺省与显式 false，默认只在请求归一化时应用，写库保证 false 被保留；同时验证账号和渠道两端与重启后状态。

### F5 — HIGH：连接探测不使用绑定出口，直接访问上游

位置：`internal/op/sub2api.go:197`。裸 http.Client 未解析 ProxyNodeID，也未复用项目代理 transport。实际会使用默认 transport 的网络路径（可能受系统代理环境变量影响），但不会走账号明确绑定的节点，不能视为满足“防关联出口绑定”。

复现：绑定不存在的节点 99999，仍直接命中本地模拟上游并返回结果。

修改方向：复用实际转发的出口解析和 HTTP client 构造逻辑。已绑定但节点缺失/未就绪必须报错且不能访问目标；验证代理可见请求、源出口、取消与超时。sync_channel=false 的账号也需要计入节点引用检查，避免节点删除后留下悬空绑定。

### F6 — HIGH：探测协议不匹配，401/500 以及解密失败可能被显示为成功

位置：`internal/op/sub2api.go:198`、`:205`、`:222`；`web/src/components/modules/account/index.tsx:56`、`:103`。探测统一拼 /v1/models，只发送 Bearer，不应用 Custom Headers；没有按认证和协议决定请求。BaseURL 已含 /v1 的两个内置预设会探测到 /v1/v1/models。非 2xx 仍返回 nil error，前端 toast.success 和绿色勾选；错误分支写入“异常”后，卡片容器仍使用绿色勾选样式。凭据解密/JSON 解析失败被忽略，继续无认证探测。

复现：本地模拟上游收到了 /v1/v1/models、没有配置的自定义头，返回 401 后 op 仍返回 nil error。把 credentials 损坏后，探测仍发出无认证请求，遇到返回 200 的站点就成功。

修改方向：区分网络可达、认证有效、模型发现、推理可用；统一规范化 URL 与协议路径，复用正式鉴权/请求头/出口；解密失败不发请求。响应至少包含 stage、ok、upstream_status、safe_error、latency_ms；200 HTML 不得视为模型接口有效。推理探针显式提示可能消耗额度。

### F7 — HIGH：完整 JSON 备份不包含 Sub2API 账号

引入点：`internal/model/sub2api.go:35` 新增持久实体，但未同步 `internal/model/backup.go:7`、`internal/op/backup.go:22` 的导入导出契约。

复现：创建非物化账号后 DBExportAll 成功，序列化结果不含账号名称或 sub2api_accounts。新实例从此 JSON 恢复时会丢失这些账号；物化渠道可能保留，但账号记录、关联与非物化凭据不能从该备份恢复。本发现特指应用 JSON 备份，不表示整库文件备份也丢失新表。

修改方向：扩展 dump 和导入，使用只在备份场景携带 Credentials 密文的 DTO；补同密钥恢复、不同密钥告警、旧备份兼容、channel_id/proxy_node_id 引用完整性和幂等导入测试。备份版本是否递增需按现有兼容规则决定。

### F8 — HIGH：高级鉴权头绕过凭据加密并经普通接口返回

位置：`internal/op/sub2api.go:73`、`:102`；`internal/model/sub2api.go:47`。headers 可接收 Authorization、Cookie、x-api-key 等敏感字段，但被保存到 Extra 明文 JSON；列表与创建响应直接返回 Extra。同步渠道时又复制到 CustomHeader。主要 api_key 的加密不能保护这些副本。

复现：提交测试用 Authorization 头，值原样存在 Extra 并出现在普通 JSON 响应中。路由有管理鉴权，未发现匿名读取；问题是凭据静态保护和普通读接口的数据最小化，不将其定性为已发生的外部泄露。

修改方向：敏感头纳入凭据密文/引用；普通 DTO 只返回头名称、掩码和 has_credentials，清除 Extra 与物化渠道中的明文秘密副本。不能只隐藏 Extra 的 JSON 而保留数据库明文。补数据库、接口响应、错误日志无测试标记的断言。

### F9 — MEDIUM：账号页丢失滚动容器，小屏及账号较多时内容可能被截断

位置：`web/src/components/modules/account/index.tsx:373` Account 根容器；外部 `web/src/components/app.tsx:152`、`app-shell.tsx:157` 为受限高度且 overflow-hidden。原账号页子面板拥有 flex/min-h-0/overflow-auto，新根容器只有 container/py/px，没有恢复滚动区域。

证据等级：静态布局分析，未做浏览器截图/交互验收。修改方向：恢复明确高度约束下的纵向滚动容器；按 390×844、1366×768 和多账号列表验收表单提交、探测、删除操作可达性。新中文文案全部写死，也需要恢复 en/zh_hans/zh_hant 资源。

### F10 — MEDIUM：请求枚举和边界缺少校验，缺少可维护的生命周期接口

位置：`internal/op/sub2api.go:52`、`:78`；`internal/server/handlers/sub2api.go:15`。仅校验 Platform/BaseURL，AccountType、APIProtocol、AccountMode 可以存任意值，负 RateLimit、无效节点也可进入数据库；空白 Name 通过 binding required 后再 trim 成空值。API 只有 list/create/delete/probe，没有改地址、换 key、启停、重试同步入口；前端 DTO 也漏了后端已返回的 channel_id。

修改方向：op 层集中校验枚举、非空名称、限流范围与节点引用；增加受校验的更新/密钥轮换、启停、同步重试接口。区分 400、404、409 和内部故障；不要把查询库异常都映射成不存在。渠道列表与账号列表的 Query 缓存同时失效。

## 验证记录与限制

| 检查 | 结果 | 能证明的范围 |
| --- | --- | --- |
| `go test ./...` | 通过 | 当前仓库已有 Go 套件 |
| `go test ./internal/op ./internal/db ./internal/server/handlers` | 通过 | 相关包现有测试 |
| web 下 `pnpm exec tsc --noEmit` | 通过 | TypeScript 静态检查 |
| web 下 `pnpm lint` | 通过，无输出警告 | ESLint |
| 独立观察套件，8 个子场景 | 8/8 复现 | 内存 SQLite、本地 httptest、删除错误注入 |

观察套件的 PASS 表示**缺陷已被复现**，不代表功能验收通过。源码归档在 `sub2api-round1-evidence/sub2api_review_observation_test.go.txt`；实际输出为 `sub2api-round1-evidence/observations.txt`。复现时临时放到 internal/op 后执行 `go test ./internal/op -run '^TestReviewSub2APIObservations$' -v -count=1`，归档后不进入日常测试。

没有访问真实订阅/生产数据库，没有调用真实付费上游，没有改动业务代码、现有数据库或发布件。未做浏览器交互、生产前端构建、race 或 MySQL/Postgres 专项验证。审查范围以本地代码和可重复的隔离测试为准，不把平台名称、HTTP 可达或绿色测试当作九个平台端到端成功的证据。

## Round 2：外部网关接入闭环与可靠性

### 范围建议（待用户/实施方确认的产品决策）

Round 2 以“外部 Sub2API 网关适配器”为目标：OctoNexus 连接已部署网关，由该网关负责订阅授权、refresh 和平台特化；OctoNexus 负责凭据、出口、能力映射、模型、授权、选路、故障转移及观测。首批承诺 OpenAI Chat、Responses、Anthropic Messages 三种网关协议；Gemini 原生需单独声明和验证。不要根据 platform 名称推导全部协议或强制使用官方平台方言，尤其是外部网关已做协议转换时。

原官方账号数据继续保留；是否保留旧入口或提供迁移向导按用户需要决定。不要为了“平滑升级”自动把旧 OAuth token 解释成外部网关 key。九个平台的本地 OAuth、token 自动刷新和浏览器会话桥接建议作为单独的后续项目。

### 建议执行顺序和交付契约

以下工作量是单人专注开发的初估，包含必要测试；不是已承诺交付日期，也未创建或执行任务。

| 工作包 | 优先级 / 估时 | 修改范围与具体交付 | 验收门槛 |
| --- | --- | --- | --- |
| R2-01 明确配置契约 | P0 / 0.5 日 | model/op/handler/前端 DTO；外部网关模式、认证、协议集合、模型来源、同步状态；更新 UI 文案 | 每个可选配置对应实际行为；不支持项不可选；保留旧数据；产品范围明确 |
| R2-02 一致性和生命周期 | P0 / 1–1.5 日 | op/channel.go 抽事务原语；sub2api 创建/更新/启停/删除/重试；稳定关联、唯一所有权、提交后缓存刷新；修复 enabled | 同名重试/并发创建、子表写失败、关联回写失败、缓存刷新失败、删除失败均无无提示残留；显式 false 和重启状态正确 |
| R2-03 正式链路与模型授权 | P0 / 1–1.5 日 | 模型输入/可选发现；映射 Grant.Protocols；复用 Group/relay；BaseURL 路径规范化；前端 channel_id 和渠道跳转 | 三种协议分别完成创建→模型/授权→分组→真实 relay 端口调用；非流式和 SSE；跨协议至少一条；停用不参与选路 |
| R2-04 出口、鉴权和探测 | P0 / 1 日 | 共享 client/request builder；绑定出口不可用时拒绝；敏感头密文/引用；结构化分阶段探测 | 代理记录到请求、目标不被直连；401/403/429/5xx、200 HTML、坏密文、超时/取消、/v1 前缀全部有断言；错误时不发匿名降级请求 |
| R2-05 备份和恢复 | P0 / 0.5–1 日 | dump/导入 DTO、密文、channel/proxy 引用验证及旧版本兼容 | 新实例 JSON 往返保留账号/凭据/绑定并恢复可调用；异密钥如实报错；重复导入不复制渠道 |
| R2-06 管理面收口与发布证据 | P1 / 0.5–1 日 | 编辑/换 key/启停/同步错误/渠道链接；双列表缓存；滚动、三语；错误状态与探测分级 | 小屏和桌面交互可达；失败能定位和修复；退出/重启保留；前端 build+Go build+隔离安装冒烟 |

估计总量 5–6.5 人日，先完成 P0 后扩展体验。新增开发任务可按这六个工作包拆分；当前仅为规划，没有开启托管或安排自动执行。

### 必须转为正式回归测试的场景

1. sync_channel=false 只建账号；true 建齐模型、协议授权和可调用分组；失败状态可见。
2. enabled 省略、true、false 的创建/更新/重启；禁用关联渠道后不再被选中。
3. 同名、相同请求重试、并发创建；强制渠道/关联写入/缓存刷新/删除失败后的数据库与缓存一致性。
4. /、/v1、有路径前缀三类地址，分别验证 Chat、Responses、Messages 的实际请求路径/认证头/协议形状。
5. 网关断连、401/403、429 Retry-After、5xx、200 HTML、错误 JSON、坏密文、取消与超时；不误报健康、不偷偷直连。
6. 带工具调用的 SSE 首帧、结束标记、客户端取消、首帧后断流，复用现有 relay 的重试/记账契约。
7. 健康网关与失败网关组成同一分组，验证故障转移和统计归属，而非只检查渠道行数。
8. 敏感头和 key 不出现在普通列表/日志/Extra 明文；备份只按明确格式保存密文。
9. 备份→全新实例→同密钥恢复→真实 relay 调用；不同密钥、旧备份和悬空关联有清晰结果。
10. 前端三语、390×844/1366×768、多账号、同步失败/未同步/可调用状态与后端一致。

Round 2 放行要求：F1–F8 关闭、F9/F10 完成或有明确可接受范围；上述矩阵及全量现有测试通过；提交前端构建/后端构建与隔离安装证据。若真实外部网关尚未提供，应明确把端到端验收记为待验，不得宣布完整接入。
