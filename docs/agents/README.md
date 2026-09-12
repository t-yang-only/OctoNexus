# 多 Agent 协同登记台账

> 位置：`docs/agents/` ＋ `docs/worklog/`
> 目标：多个 Agent（人 / Cursor / Codex / 其他）在同一仓库协作时，**可登记、可认领、可追踪、不打架**。

## 目录结构

```text
docs/agents/
  README.md            # 本文件：协议 + 索引
  registry.json        # Agent 注册表（谁在协作）
  ledger.md            # 任务登记台账（活文档，每次认领/更新都改它）
  需求登记.md          # 需求登记表（待确认→已确认→已立项，确认后拆入 ledger）
  claims/              # 每条认领一条文件，避免同时改 ledger 冲突
    YYYY-MM-DD-<agent>-<task>.md
```

`docs/worklog/` 保持不变：每次工作完写一篇日志，命名 `YYYY-MM-DD-<主题>.md`。

## 角色

| 角色 | 职责 |
|------|------|
| reporter | 登记任务（写 `ledger.md` 一行 + 可选建 claim） |
| worker | 认领（建 `claims/` 文件 + 改 ledger 状态为 `doing`），完工改 `done` 并写 worklog |
| reviewer | 验收 `done` 任务，不通过打回 `todo` 并写原因 |

## 任务状态机

```text
todo → doing → review → done
  ↑       │        │
  └───────┴────────┘（打回）
```

`blocked` 可从 `todo/doing` 进入，解决后回到原状态。

## 协议（所有 Agent 必须遵守）

1. **先读后写**：开工前读 `registry.json` + `ledger.md` + 自己的 claim 文件。
2. **认领加锁**：同一任务同一时间只能有一个 `doing`。认领时新建
   `claims/YYYY-MM-DD-<agent-id>-<task-id>.md`，若文件已存在则任务已被认领，换任务或联系对方。
3. **小步提交**：完工一项就更新 `ledger.md` 状态 + 写 `docs/worklog/` 日志，commit 信息带任务 ID。
4. **密钥红线**：`数据备份.json`、任何 API Key / token **绝不贴原文、绝不提交**。
   日志只记结构与数量。
5. **上游干净**：与 `origin/master` 同名文件冲突时，先备份到 `docs/` 再还原上游，保持 `git diff` 干净。
6. **索引同步**：新增 worklog / claim 后，同步更新 `docs/worklog/README.md` 索引和本文件索引。

## 任务 ID 规范

`T-<模块>-<序号>`，如 `T-relay-001`、`T-web-002`、`T-docs-003`。

## 索引

- 注册表：[`registry.json`](./registry.json)
- 台账：[`ledger.md`](./ledger.md)
- 需求登记：[`需求登记.md`](./需求登记.md)
- 研究报告：[`../research/`](../research/)（对标研究等，一主题一文件，命名 `YYYY-MM-DD-<主题>.md`）
- 工作日志规范：[`../worklog/README.md`](../worklog/README.md)
