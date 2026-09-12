# 认领：T-research-003 litellm router_strategy/cooldown 精读

- Agent: `cursor-local`（coordinator）
- 认领日期：2026-09-13
- NM 编号：NM-CUR-021
- 任务：精读 `reference/litellm/litellm/router_strategy` + `router_utils/cooldown_handlers.py`，输出 `pickGroupItem` 扩展设计稿
- 状态：done

## 计划

1. [x] 读 `lowest_cost.py`（分钟级 token 成本累计选路）
2. [x] 读 `least_busy.py`（在途计数选路）与 `lowest_tpm_rpm_v2.py`（RPM/TPM 选路）
3. [x] 读 `cooldown_handlers.py`（失败率阈值+分异常类型容忍度）
4. [x] 产出 `docs/research/2026-09-13-T-research-003-路由策略设计稿.md`
   （GroupMode 新增 lowest_cost/lowest_latency/least_busy + strategy.go 打分骨架，先做 lowest_cost）
5. [x] 更新 ledger 状态 done
