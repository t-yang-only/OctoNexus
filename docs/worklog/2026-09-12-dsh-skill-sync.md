# 2026-09-12 从 DSH 同步 nm-skills

- 仓库：https://github.com/bestruirui/octopus
- 本地：`d:\奇怪的软件\octopus`
- 分支：`master` 跟踪 `origin/master`
- 台账编号：`NM-CUR-001`（`agent_word/工作登记表.md`）
- 同步源：`C:\Users\t-yang\.dsh\skills\nm-skills\`（DSH 实时目录，与 `D:\DSH-Backup-20260911-162008\.dsh\skills\nm-skills\` SHA256 一致）

## 目标

用户要求“从 DSH 同步 skill 过来”，上一轮 `/nm-skills` 无法解析（本仓 `Glob **/*skill*` 0 命中）。
本次定位 DSH skills 目录，把 `nm-skills` 同步进本仓，并按其自身规范完成 `agent_word` 登记。

## 前置调查

1.  sibling 目录发现 `D:\DSH-Backup-20260911-162008\.dsh\skills\`（72 个 skill，含 `nm-skills/skill-sync/...`）与 `d:\奇怪的软件\skills-Management\`（团队 skill 仓，`word/skills/` 含 `skill-sync` 等）
2.  确认实时 DSH 目录 `%USERPROFILE%\.dsh\skills\` 与该备份的 `nm-skills` 文件 SHA256 完全一致：
   - `SKILL.md`：`639C28E3...58842889E`（7025 字节）
   - `scripts/nm_register.py`：`77D6B7D6...304FF5`（8835 字节）
3.  确认 `nm-skills` 内容：多 Agent 协作台账规范，工程根下维护 `agent_word/`（README/工作登记表/工作日志/技能建议/API变更/文件清单 6 件套），编号 `NM-<客户端>-<序号>`，附 `scripts/nm_register.py` 自动登记脚本
4.  确认三端已有同版 `nm-skills`：`~/.dsh/skills`、`~/.cursor/skills`、`~/.codex/skills` 的 `SKILL.md` 均为同一 hash；`~/.cursor/skills` 比 DSH 少 `ppt-skill/taskpkg-*` 等 13 个，其余一致
5.  确认本仓此前无 `skills/`、`agent_word/`，`docs/agents/` 是上一轮自建的另一套台账（`ledger.md` 有 T-docs-001/T-sec-001/T-env-001/T-agents-001），与 `nm-skills` 的 `agent_word/` 并存，尚未合并

## 执行操作

1. `New-Item skills\nm-skills\scripts`，`Copy-Item` 源两文件（跳过 `__pycache__/`）到本仓
2. `Get-FileHash` 双向校验：目标两文件 hash 与源完全一致
3. 运行 `python "skills\nm-skills\scripts\nm_register.py" --client CUR --task "从DSH同步nm-skills到octopus仓库" ... --status done`，自动生成 `agent_word/` 6 文件并登记为 `NM-CUR-001` 已完成

## 结果

- 新增：`skills/nm-skills/SKILL.md`、`skills/nm-skills/scripts/nm_register.py`（hash 与 DSH 源一致）
- 新增：`agent_word/`（`README.md`、`工作登记表.md`、`工作日志.md`、`技能建议.md`、`API变更.md`、`文件清单.md`），首行 `NM-CUR-001 已完成`
- commit：（待提交时填写 hash）
- `git status --short` 新增：`skills/`、`agent_word/`（待下条命令复核）

## 遗留问题

- 两套台账并存：`docs/agents/`（ledger/registry/claims）vs `agent_word/`（nm-skills 规范）。建议后续二选一：若团队用 nm-skills，则把 `docs/agents/ledger.md` 的 4 个任务迁移进 `agent_word/工作登记表.md` 并归档 `docs/agents/`；若保留自建台账，则 `agent_word/` 仅作 skill 运行产物
- `skills/` 是否纳入 git 跟踪未定：本仓 `.gitignore` 是上游 Go 版（`data/build/static/out/*/.vscode`），`skills/` 不在忽略内，`git add` 会带入；若只想本地使用，需追加忽略或放 `docs/` 外
- T-sec-001 仍最高优：`数据备份.json` 含明文 key 在工作区，绝不提交，需移出 + 轮换
- `skills-Management` 仓有 `skill-sync` 标准流程（`verify.ps1` + `sync.ps1` + `skm`），本仓直接文件拷贝，未走该流程；若后续要回供给团队仓，需按其清单操作

## 下一步

1. 用户定台账方向（二选一），我做迁移/归档
2. 定 `skills/` 跟踪策略（提交 vs 忽略）
3. 继续 `ledger.md` 的 T-sec-001 → T-docs-001 → T-env-001
4. 更新 `docs/worklog/README.md` 索引
