# 认领：T-acct-001 官方账号授权接入（OpenAI/Gemini/Claude 扫码+套餐/健康/窗口读取）

- 认领 Agent：cursor-local（NM-CUR-128）
- 认领时间：2026-09-13
- 登记台：`docs/agents/ledger.md` T-acct-001（todo，无认领）
- 前置需求：R-acct-001（用户原话：官方账号三类经官方授权链接接入，扫码后自动读取套餐档位/健康与 5H、7D 可用窗口）
- 范围：只做接入+读取（OAuth/扫码凭据加密存，刷新续期）；不含转发（T-pool-002）、
  不含余额采集定时任务（T-quota-001）、不含 key 健康探测（T-acct-003）
- 计划：
  1. `internal/model`: 官方账号实体（provider/owner/套餐/窗口/健康）+ 凭据加密字段设计稿
  2. 接入骨架：授权链接生成→回调/扫码确认→token 落库（加密）→套餐档位/健康/窗口读取接口
  3. 与 ChannelKey 建模关系：官方账号凭据复用渠道凭据形态（T-pool-002 消费本任务产出）
  4. 设计先行：凭据加密口径关联 R-sec-001/U-sec-001；OAuth 安全红线（state/nonce/PKCE）
  5. 产出：模型文件 + 设计文档 + 单测；不改现有转发链路
