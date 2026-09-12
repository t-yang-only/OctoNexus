# 工作日志（Worklog）

> 仓库：https://github.com/bestruirui/octopus
> 本地路径：`d:\奇怪的软件\octopus`
> 分支：`master`（跟踪 `origin/master`）

## 规范

- 每篇日志一篇文件，命名：`YYYY-MM-DD-<主题>.md`
- 日志必须包含：目标、执行命令/操作、结果（含 commit hash）、遗留问题、下一步
- 涉及密钥（API Key、token、备份 JSON）绝不贴原文、绝不提交
- 与上游同名文件冲突时，先备份到 `docs/` 再还原上游，保持 `git diff` 干净

## 索引

- [2026-09-12 仓库拉取与初始化](./2026-09-12-repo-bootstrap.md)
- [2026-09-12 多 Agent 协同登记台账](./2026-09-12-agents-ledger.md)
- [2026-09-12 从 DSH 同步 nm-skills](./2026-09-12-dsh-skill-sync.md)
- [2026-09-12 响应 nm-skills 调用并汇报台账状态](./2026-09-12-nm-skills台账响应.md)
- [2026-09-12 Skills 搜索收尾（NM-CUR-003/NM-CUR-004）](./2026-09-12-skills搜索收尾.md)
- [2026-09-13 渠道模型自动创建渠道名/模型名分组](./2026-09-13-渠道模型自动创建分组.md)
- [2026-09-13 拉取 litellm 仓库到本地](./2026-09-13-拉取litellm.md)
- [2026-09-13 拉取 api-monitor 仓库到本地](./2026-09-13-拉取api-monitor.md)
- [Python 原型备份说明](../python-prototype-README.backup.md)（本地 Python 版 README，已与上游 Go 版区分存放）
- [多 Agent 协同登记台账](../agents/README.md)（协议 + `registry.json` + `ledger.md` + `claims/`）
