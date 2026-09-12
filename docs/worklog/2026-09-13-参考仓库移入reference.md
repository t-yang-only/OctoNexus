# 2026-09-13 外部参考仓库移入 reference/

- 仓库：https://github.com/bestruirui/octopus（本地 `d:\奇怪的软件\octopus`）
- 台账编号：`NM-CUR-014`
- 分支：`master` 跟踪 `origin/master`
- 触发：用户 `/nm-skills` 并要求“把拉取的项目移动到本项目文件夹内”

## 目标

把此前拉到 `d:\奇怪的软件\` 下的两个参考仓库（`litellm`、`api-monitor`）移入 octopus 项目文件夹内统一管理，同时保证：

1. 两个参考仓各自 `.git` 完整可用（仍是独立仓库）。
2. 不污染 octopus 的 `git status`（嵌套仓库不入库）。
3. 台账登记与 worklog 同步。

## 冲突检查

- 读取 `agent_word/工作登记表.md`：CUR 最大序号 013（011/013 进行中），本次分配 `NM-CUR-014`。
- `d:\奇怪的软件\` 下新增过 `openmux`（其他 Agent/用户操作，与本任务无关），`litellm`/`api-monitor` 仍在原位。
- `internal/server/handlers/` 等文件已被进行中任务（NM-CUR-011）修改，本任务不触碰这些文件，无冲突。

## 执行操作

1. 确认待移动仓库与大小：`litellm` 238.3MB（浅克隆）、`api-monitor` 2.7MB（完整克隆）。
2. 询问用户落点，用户选择“仓库根目录新建 reference 文件夹，放这下面”。
3. 记录移动前基线：`git status --short --branch`（21 个 M + docs/ 等未跟踪，见 NM-CUR-011/013 等任务）。
4. 移动（同一盘符跨目录 `Move-Item`，秒完成）：

```powershell
New-Item -ItemType Directory -Force "D:\奇怪的软件\octopus\reference" | Out-Null
Move-Item "D:\奇怪的软件\litellm" "D:\奇怪的软件\octopus\reference\litellm"
Move-Item "D:\奇怪的软件\api-monitor" "D:\奇怪的软件\octopus\reference\api-monitor"
```

5. 验证移动后仓库完整：
   - `git -C reference/litellm log --oneline -1` → `1c61c26`（与拉取时一致）
   - `git -C reference/api-monitor log --oneline -1` → `c8731a3`（与拉取时一致）
   - 父目录 `d:\奇怪的软件\` 已无 `litellm`/`api-monitor`。
6. `.gitignore` 追加（该文件此前已有本地改动，本次只追加一段）：

```gitignore
# 外部参考仓库, 不入库
reference/
```

7. 验证忽略生效：`git check-ignore -v reference/litellm reference/api-monitor` → 均命中 `.gitignore:17:reference/`；`git status` 不再出现 `reference/`。
8. 运行登记脚本：
   `python "skills\nm-skills\scripts\nm_register.py" --client CUR --task "外部参考仓库移入项目内并忽略" --changes "移动litellm与api-monitor到octopus/reference/并保持各自git完整,.gitignore追加reference/防嵌套仓库入库" --api "无" --files "reference/litellm,reference/api-monitor,.gitignore,docs/worklog/2026-09-13-参考仓库移入reference.md,docs/worklog/README.md" --skills "nm-skills" --mcps "无" --tools "Read,Shell,Write,AskQuestion" --status done`
   输出 `NM-CUR-014`。
9. 新增本文件；更新 `docs/worklog/README.md` 索引；补 `agent_word/文件清单.md`。

## 结果

- 移动：`d:\奇怪的软件\octopus\reference\litellm`（浅克隆，HEAD `1c61c26`）、`d:\奇怪的软件\octopus\reference\api-monitor`（完整克隆，HEAD `c8731a3`），两仓 `.git` 完整可独立操作。
- 忽略：`.gitignore` 第 17 行 `reference/` 生效，octopus `git status` 不含参考仓库。
- 登记：`工作登记表.md` 新增 `NM-CUR-014 已完成`；`工作日志.md` 追加对应小节。
- API 变更：无（`API变更.md` 保持空表）。
- commit：（octopus 仓未提交，`.gitignore` 改动随既有本地改动一并待提交；reference/ 不入库）

## 遗留问题

- `reference/litellm` 仍是浅克隆且默认分支为 `litellm_internal_staging`；需要完整历史时在 `reference/litellm` 内 `git fetch --unshallow`。
- 嵌套仓库未用 `git submodule` 管理：reference/ 整体忽略，适合纯本地参考；若以后要让团队拿到同一份参考源码，再决定是否改为 submodule 或 vendored 子目录。
- `数据备份.json` 仍在工作区（已被 .gitignore 忽略），含明文 key，绝不提交，需移出并轮换（最高优）。
- 进行中任务 NM-CUR-005/008/011/013 与本任务无交集。

## 下一步

1. 按 reference/litellm 与 reference/api-monitor 对照 octopus 渠道/中继/监控实现。
2. 继续进行中任务（005/008/011/013）与 T-sec-001。
