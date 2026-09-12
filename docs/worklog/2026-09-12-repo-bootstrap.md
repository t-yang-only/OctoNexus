# 2026-09-12 仓库拉取与初始化（repo bootstrap）

- 仓库：https://github.com/bestruirui/octopus
- 本地：`d:\奇怪的软件\octopus`
- 分支：`master` 跟踪 `origin/master`
- HEAD：`d5a893f feat: :sparkles: 全局模型过滤`（2026-09-10）
- 上游标签：`v0.13.4` 等（`git tag --sort=-v:refname` 最新 5 个需复核）

## 目标

1. 把空工作目录与上游 `bestruirui/octopus` 对齐为可用的 git 工作区
2. 识别本地未提交/未跟踪内容与上游的冲突
3. 建立 `docs/worklog/` 工作日志规范

## 执行过程

1. 检查工作区：`Get-ChildItem -Force` 仅有 `.vscode/settings.json({})`，`git status` 报 `not a git repository`
2. `git clone ... .` 失败：`destination path '.' already exists and is not an empty directory`（因 `.vscode` 占位）
3. 改用 `git init + git remote add origin + git fetch origin`，拿到 `origin/master` 与全部 `v0.0.1~v0.13.4` 标签
4. `git checkout -b master --track origin/master`，`git reset --hard origin/master` 恢复上游文件：
   `cmd/` `internal/` `web/` `static/` `scripts/` `main.go` `go.mod`（Go 1.26.4）等
5. 发现本地冲突（`git status --short`）：
   - `M .gitignore`：上游为 `data/build/static/out/*/.vscode`，本地被改成 Python 版（`.venv/__pycache__/dist/...`）
   - `M README.md`：上游为 Go 版 LLM 聚合服务文档（370+ 行），本地被改成 Python 版 `Modular multi-tool automation framework`（55 行）
   - 未跟踪：`src/octopus/`（`cli.py/config.py/pipeline.py/plugin.py/__init__.py/plugins/__init__.py`）、`tests/test_pipeline.py`、`pyproject.toml`、`数据备份.json`（364KB，含 channels/keys/groups/stats，内有大量 `sk-` 明文 key）
6. 处理：
   - `docs/` 新建，把本地 Python 版 `README.md` 备份为 `docs/python-prototype-README.backup.md`
   - `git show origin/master:README.md` / `.gitignore` 还原上游版本
   - `git checkout --` 清掉剩余 BOM/换行差异，`git diff` 归零（vs 上游干净）
   - 尝试 `.git/info/exclude` 本地忽略 `数据备份.json`（含线上密钥，绝不提交）；`pyproject.toml + src/ + tests/` 为本地 Python 原型，暂保留未跟踪，待用户决定拆仓/迁移
7. 建立工作日志：
   - `docs/worklog/README.md`（规范 + 索引）
   - 本篇 `2026-09-12-repo-bootstrap.md`

## 结果

- `git status --short --branch`（相对上游）：仅剩未跟踪 `docs/ src/ tests/ pyproject.toml 数据备份.json`，无 `M` 修改
- `git log --oneline -5`：`d5a893f / 6dce286(v0.13.4) / 1bd2ed8 / 9a80de3 / 0b919da`
- 项目识别（上游 Go 版）：LLM API 聚合（多渠道聚合、OpenAI/Anthropic 协议互转、价格同步、故障转移、请求可视化、统计、单文件部署，SQLite/MySQL/PG），前端 `web/`（React19+Vite+Tailwind），后端 `internal/relay|server|op|model|db|price|task|...`
- 本地 Python 版识别：`src/octopus` 插件式 pipeline（`Plugin+@register`、`Pipeline(steps,params)`、`YAML/JSON/TOML config`、`argparse CLI list/run/init`），与上游 Go 工程无关，需分开存放

## 安全提醒（重要）

- `数据备份.json` 含大量线上渠道 `key`（如 `sk-81af...`、`sk-octopus-...`）与统计数据，已提醒绝不 `git add/commit/push`，建议移出仓库目录并轮换密钥
- 日志中只记结构与数量，不贴密钥原文

## 遗留问题

- `/nm-skills`：本仓库内 `Glob **/*skill*` 0 命中，可用 skills 列表为空，无法解析该指令意图；暂按“工作日志”部分交付，若指的是某 skill 目录/命令请给出全称
- 本机无 `go`（`go version` 报 CommandNotFound），未做构建验证；前端需 Node18+pnpm
- `pyproject.toml/src/tests` 去留未定：与上游同名（`README.md/.gitignore`）已解决，但目录级共存仍混乱
- `.git/info/exclude` 对中文文件名可能编码不生效，`数据备份.json` 仍显示为 `??`，需改用 UTF-8 无 BOM 重写或改名移出

## 下一步

1. 决定 Python 原型去向：A) 移到 `python-prototype/` 子目录 B) 另建仓库 C) 删除；确定后更新 `docs/worklog`
2. 把 `数据备份.json` 移出工作区（如 `D:\备份\`），并轮换其中密钥
3. 安装 Go 1.26 + Node18 + pnpm 后跑 `web/pnpm install && pnpm run build` + `go run main.go start` 验证
4. 后续每次工作新增 `docs/worklog/YYYY-MM-DD-<主题>.md` 并更新索引
