# Project Instructions

OctoNexus（原 Octopus）：个人自用的 LLM API 聚合网关，Go 后端 + React 前端，单二进制部署。
上游仓库 https://github.com/bestruirui/octopus ，本地二次开发的改动记录在 `docs/worklog/`。

## Tech Stack

- 后端：Go 1.26（gin + gorm，多数据库 SQLite/MySQL/Postgres，axonhub/llm 做协议转换，cobra + viper）
- 前端：React 19 + TypeScript + Vite 8 + Tailwind 4 + Radix UI（shadcn 风格）+ zustand + TanStack Query，pnpm
- 构建：前端产物输出到 `static/out/`，经 `static/static.go` go:embed 进二进制 —— 改前端后必须先 `pnpm build` 再跑后端

## Build & Run

- 前端 dev：`cd web && pnpm install && pnpm dev`（Vite 代理 `/api` 到 admin 端口 3303）
- 后端 dev：`go run main.go start`（配置读 `data/config.json`，默认 admin 3303 / relay 1234 双端口）
- 生产构建：`cd web && pnpm build`，然后 `go build -trimpath -tags=jsoniter .`
- Go 测试：`go test ./...`（现有测试：`internal/op/auto_group_test.go`、`internal/server/router/router_test.go`）
- 前端 lint：`cd web && pnpm lint`；构建自带 `tsc --noEmit`
- CI（GitHub Actions）要求 `python3 scripts/updatePrice.py` 可执行（更新价格预设）

## Project Structure

- `main.go`、`cmd/` — 入口与 cobra 命令（start/version）；启动顺序：conf → db → op 缓存 → server → task
- `internal/conf/` — viper 配置（`server.admin_port`/`server.relay_port`，env 前缀 `OCTOPUS_`）
- `internal/db/` — gorm 初始化与 `db/migrate/` 手写编号迁移（001–012，新迁移递增编号）
- `internal/model/` — 数据库实体与 API DTO（Group/Channel/ChannelGrant/APIKey/Stats 等）
- `internal/op/` — 业务操作层，各类实体带进程内缓存（`utils/cache`），写库后刷新缓存
- `internal/relay/` — 核心转发：请求状态机（state）、按分组选路（route）、上游调用（upstream）、协议判定（protocol）
- `internal/server/` — gin 服务：`router/` 声明式路由注册表，`handlers/` 每个 handler 在 `init()` 里注册路由，`middleware/`，`resp/` 统一 `{code,message,data}` 响应
- `internal/server/router/group.go` — `ServerAdmin`/`ServerRelay` 区分双端口挂载，`ServeOn()` 指定归属
- `internal/task/` — 定时任务（价格同步、统计落盘）；统计先写内存，按间隔批量入库，退出需优雅关停
- `internal/price/` — 从 models.dev 同步模型价格
- `web/src/` — 前端：`api/`（TanStack Query 封装）、`components/modules/`（channel/group/model/log/setting 等页面）、`components/ui/`（shadcn 组件）、`stores/`（zustand）、`locales/`（i18n：en/zh_hans/zh_hant）
- `scripts/` — 构建脚本（build.sh、Dockerfile、updatePrice.py）
- `static/` — 嵌入产物（`static/out/` 已 gitignore）

## Core Concepts（改业务前必读）

- **Channel → Grant → Group** 三层模型：渠道含凭据（Key）与模型（Model），两者组合成授权（Grant，带协议位掩码 Protocol）；分组（Group）把若干 Grant 聚合成对外的模型名。客户端请求的 `model` 参数即分组名
- **协议位**（`model.Protocol` 位掩码）：OpenAI Chat `1<<1`、OpenAI Responses `1<<2`、Anthropic Message `1<<3`，位值已落库不可变更
- **转发循环**（`internal/relay/handler.go` `Forward`）：解析请求 → 按分组名选成员 → 每轮重读分组配置 → 同协议直通/跨协议经 axonhub pipeline 转换 → 失败按 RelayConfig 重试/冷却/故障转移，首字节提交后不可重试
- **双端口**：admin 端口承载管理面板与 `/api/v1/*`，relay 端口只承载 `/v1/*` 转发接口；新增路由组时用 `ServeOn` 明确归属

## Code Style

- Go：中文注释解释「为什么」而非「做什么」；实体用 struct 平铺 + gorm tag；DTO 与库内实体共用定义（如 ChannelConfig）
- Go 错误处理：`fmt.Errorf("...: %w", err)` 包装；HTTP 层统一走 `resp.Error/Success`
- 前端：函数组件 + hooks；路径别名 `@/` → `web/src/`；i18n 文案进 `locales/*.json`，不写死
- 路由注册：handlers 在 `init()` 里 `router.NewGroupRouter(path).ServeOn(...).Use(...).AddRoute(...)`，不手写 gin 引擎代码

## Conventions & 合作规范

- 多 Agent 协作：开工前先读 `agent_word/工作登记表.md`，按 `skills/nm-skills/SKILL.md` 登记编号与日志
- 每次实质工作写 `docs/worklog/YYYY-MM-DD-<主题>.md` 并更新其 `README.md` 索引
- 密钥红线：`数据备份*.json`、API Key、token 绝不贴原文、绝不提交
- 与上游同名文件冲突时先备份到 `docs/` 再还原上游，保持 `git diff` 干净
- Git：commit 风格沿用上游 `feat:/:bug:/build:` 前缀，可中文描述

## Where to Look

| 我想要… | 看… |
|---|---|
| 加管理 API | `internal/server/handlers/`（init() 注册）+ `internal/op/` 对应实体 |
| 改转发/故障转移逻辑 | `internal/relay/handler.go`、`route.go`、`model/group.go` 的 RelayConfig |
| 加数据库字段 | `internal/model/` 实体 + `internal/db/migrate/` 新编号迁移 |
| 加前端页面/组件 | `web/src/components/modules/`，API 封装进 `web/src/api/` |
| 改协议转换 | `internal/relay/protocol.go`、`upstream.go`（axonhub transformer） |
| 改配置项 | `internal/conf/config.go` + README 配置表 |
