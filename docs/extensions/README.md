# 扩展能力（R-ext-001）

octopus 的"软件内的软件"：**不改主程序**就能接新东西。面板左侧「Extensions / 扩展」进去是一层子菜单，
子菜单顺序就是排查顺序。

| 子菜单 | 回答的问题 | 后端契约 |
| --- | --- | --- |
| 插件（Plugins） | 我要接一个自己的反代工具进来 | `docs/plugin/README.md`，`/api/v1/plugin/*` |
| 采集凭据（Credential sources） | 我要看到某家中转站的余额 | `docs/collector/README.md`，`/api/v1/collector/*` |
| 号池适配器（Pool adapters） | 我要让号池认识一个新的账号池形态 | 本节下文，`/api/v1/pool/adapters*` |

三者常互为因果（插件要出口、采集要反代、适配器要主机白名单），所以放在一起而不是三个一级菜单。

## 号池预留接口（外部工具能读到的全部面）

**管理面**（需要面板登录态 `middleware.Auth()`）：

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/v1/pool/kinds` | 有哪些账号池形态 |
| GET | `/api/v1/pool/entries` | 条目清单（可按 kind 过滤） |
| GET | `/api/v1/pool/stats` · `/summary` | 统计与汇总 |
| GET | `/api/v1/pool/export` | 导出（脱敏，凭据不出现） |
| GET | `/api/v1/pool/kinds/:kind` | 单个形态的详情与能力位 |
| POST | `/api/v1/pool/kinds/:kind/sync` | 同步成渠道凭据（单向） |
| POST | `/api/v1/pool/entries/batch` | 批量操作 |
| GET/POST | `/api/v1/pool/entries/:kind/:id` · `/probe` · `/refresh` · `/enable` · `/disable` | 单条目运维 |
| GET | `/api/v1/pool/openapi.json` | 自描述契约（给外部工具与代码生成用） |
| GET/POST/DELETE | `/api/v1/pool/adapters` · `/adapters/templates` · `/adapters/:kind` | 声明式适配器注册 |

**转发口只读通道**（外部工具用 API Key 调用，写操作**不存在**于这一层）：

```
GET /v1/pool/kinds · /entries · /stats · /summary
```

### 声明式适配器的硬约束（fail closed）

- `kind` 必须以 `custom-` 开头，内置 kind 不可覆盖、不可移除。
- **主机白名单**：设置项 `pool_declarative_hosts` 为空 ⇒ 一律拒绝注册；不跟 3xx 跳转（否则白名单可被 302 绕过）。
- **只读能力位**：只接受 `list` / `get`；`sync` / `toggle` / `provision` / `revoke` / `refresh` / `probe` 一律拒绝注册。
- 超时 10s、响应体上限 1MiB；整份规格**密文落库**（AAD `pool:declarative`），缺密钥时拒绝注册而不是明文落盘。
- 对外的任何出口都过脱敏（凭据不出现在响应里）。

面板上「号池适配器」子页就是这套接口的入口：模板一键填入 → 改一改 → 注册；同时把
白名单现状、只读通道状态与预留端点清单摆在明面上。

## 三类扩展各自的不可违背点

1. **插件**：出口拿不到就拒绝启动（绝不静默直连真实 IP）；令牌只经环境变量注入，不回显。
2. **采集凭据**：包与凭据、会话三类密文用不同 AAD 分开加密；反代登录页时**octopus 自己的 `auth` Cookie 永不转发给上游**。
3. **适配器**：白名单 fail closed + 只读能力位，写操作结构上不可达。

## 诚实边界

- 面板这一层只做"看得见、点得到、改得动"，不新增后端能力；所有能力在契约文档里，第三方工具可直接调接口。
- 适配器注册后仍需在号池页做一次同步才会变成渠道凭据（同步是单向的，且只在管理面）。
- 采集包的分享/导入导出还没做（当前只能在采集凭据子页粘贴 JSON）；**怎么给一家新站点做采集包、怎么把它交给别人**见 [采集包开发指南](./采集包开发指南.md)。
