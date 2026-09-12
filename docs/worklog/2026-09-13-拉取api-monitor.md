# 2026-09-13 拉取 api-monitor 仓库到本地

- 仓库：https://github.com/bestruirui/octopus（本地 `d:\奇怪的软件\octopus`）
- 拉取目标：`https://github.com/baogutang/api-monitor`
- 落点：`d:\奇怪的软件\api-monitor`（与 octopus、litellm 同级的独立仓库，不在 octopus git 内）
- 台账编号：`NM-CUR-012`
- 分支：octopus 侧 `master` 跟踪 `origin/master`

## 目标

把 api-monitor 源码拉到本地，供对照其上游监控/资产/告警实现。

## 执行操作

1. 开始前读取 `agent_word/工作登记表.md`、`工作日志.md`、`API变更.md`、`文件清单.md`，确认 CUR 最大序号 011（011 进行中），本次分配 `NM-CUR-012`，落点在 octopus 仓外、无冲突。
2. `[System.IO.Directory]::GetFileSystemEntries("D:\奇怪的软件")` 确认父目录已有 `litellm`（上一轮 NM-CUR-010），无 `api-monitor` 残留。
3. 完整克隆：`git clone https://github.com/baogutang/api-monitor.git api-monitor`（working_directory `d:\奇怪的软件`），5.9s 完成。
4. 验证：
   - HEAD `c8731a355acd19fdbba1b51484c6d1a2fa832b30`（2026-06-25，`feat: improve upstream usage aggregation`）
   - 分支 `main`，remote `origin → https://github.com/baogutang/api-monitor.git`
   - `rev-parse --is-shallow-repository` → `false`（完整历史，无需 unshallow）
   - `count-objects -vH` → in-pack 257 对象 / 1.48 MiB
   - `go.mod` → `go 1.23`
5. 顶层结构：`cmd/ internal/ web/ migrations/ docs/ Dockerfile docker-compose.yml .env.example LICENSE README.md`，Go 后端 + web 前端 + PostgreSQL/Redis 组合。
6. 运行登记脚本（octopus 根）：
   `python "skills\nm-skills\scripts\nm_register.py" --client CUR --task "拉取api-monitor仓库到本地" --changes "完整克隆baogutang/api-monitor到D:/奇怪的软件/api-monitor,验证HEAD c8731a3与main分支,Go1.23全量历史仅1.48MiB" --api "无" --files "docs/worklog/2026-09-13-拉取api-monitor.md,docs/worklog/README.md" --skills "nm-skills" --mcps "无" --tools "Read,Shell,Write" --status done`
   输出 `NM-CUR-012`，脚本自动追加登记行、日志节并更新技能建议时间戳。
7. 新增本文件；更新 `docs/worklog/README.md` 索引；补 `agent_word/文件清单.md`。

## 项目速览（读 README 摘录）

自托管 AI API 运维控制台：监控中转站余额/额度/套餐窗口/公告/模型/价格变更，把上游当普通用户账号接入（new-api、sub2Api），不要求上游 admin 权限；支持 OpenAI/Anthropic/Gemini 官方账号与 Key 健康探测；内容源指纹比对触发 `announcement_changed`/`model_catalog_changed`/`pricing_changed` 等规则；通知通道钉钉/飞书/企业微信/Webhook/SMTP 等。Go 单服务 + PostgreSQL + Redis，Docker/NAS 友好。

## 结果

- 落点：`d:\奇怪的软件\api-monitor` 完整克隆，HEAD `c8731a3`（2026-06-25），分支 `main`，历史完整非浅克隆。
- 登记：`工作登记表.md` 新增 `NM-CUR-012 已完成`（2026-09-13 01:01:04 开始并完成）。
- 日志：`工作日志.md` 新增 `## [2026-09-13 01:01:04] NM-CUR-012` 一节。
- API 变更：无（`API变更.md` 保持空表）。
- commit：（octopus 仓未提交；api-monitor 为独立仓，不计入 octopus commit）
- `git status --short --branch`（octopus 侧）：`## master...origin/master`，4 个 `M`（config.go/channel.go/group.go/router.go），未跟踪 `agent_word/ docs/ skills/ src/ tests/ internal/op/auto_group_test.go internal/server/router/group.go scripts/check_auto_groups.py 数据备份.json`。

## 遗留问题

- `数据备份.json` 仍在 octopus 工作区且含明文 key，绝不提交，需移出并轮换（最高优）。
- NM-CUR-005/NM-CUR-008/NM-CUR-011 仍为进行中，与本次拉取无关。
- 本地 `d:\奇怪的软件` 现在并排三仓：`octopus`（主仓）、`litellm`（浅克隆对照）、`api-monitor`（完整克隆对照），后续在 octopus 内引用外部仓路径即可，勿嵌套。

## 下一步

1. 按 README 功能地图对照：api-monitor 的资产/内容源监控 vs octopus 的渠道/分组/中继。
2. 继续推进 octopus 侧进行中任务（005/008/011）与 T-sec-001。
