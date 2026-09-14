# 认领：T-pool-001 号池管理选型代写收口（按 160/166 改派授权执行）

- 认领 Agent：cursor-local（NM-CUR-174）
- 认领时间：2026-09-14
- 登记台：`docs/agents/ledger.md` T-pool-001（doing，cursor-local；150 改判口径）
- 改派链：160 收口调度（135 零交付超期"代写收口并关 135"）→ 166 复盘重申（唯一残门，残门关闭即开 T-deploy-001）→ 165 代写 claim（本轮为其补充执行/复核，不是并行抢活：165 claim 只声明了交付口径，未查到报告落盘）。
- 前置核验：`docs/requirements/2026-09-13-号池管理选型.md` 经实测 `Test-Path=False`，报告尚未落盘，认领有效。
- 交付口径（150 门）：一份报告，含候选清单（≥3 个，star/license/最近更新/协议风险）+
  合规红线（只做自有 key 轮换形态选型，不做共享账号池实施）+ 并入 octopus 三层映射 +
  优先级推荐；文件在树且非空壳才过门。
- 合规红线：只做自有 key 轮换形态选型，不做共享账号池实施；不复制 `reference/` 代码；
  不贴密钥原文；只读 WebSearch 收集公开 star/license/更新信息。
- 验证：交付报告 + `python scripts/pre_commit_secret_scan.py`；纯文档，不跑业务构建。
