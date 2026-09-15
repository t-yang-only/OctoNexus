# CLAIM: T-chan-001 渠道凭据补录与凭据启用位修复

- 任务：T-chan-001（用户新给三把渠道凭据的落地与自查；两条中转站账号登录测试）
- 认领：dsh-local（NM-DS-015），2026-09-16
- 锁定范围：`internal/model/channel.go`（凭据请求侧形状）、`internal/op/channel.go`（syncChannelKeys / channelDetail）、
  `internal/op/channel_key_test.go`（新）、`scripts/api-tests/run_group_audit.py`（新，分组与模式白名单回归护栏）、
  `docs/worklog/2026-09-16-渠道凭据补录与凭据启用位修复.md`、`docs/worklog/README.md`、`docs/agents/ledger.md`、
  `docs/requirements/2026-09-15-需求事实源整合.md`、`agent_word/*`。
- 只读不碰：用户的线上渠道数据（本地库里的 53HK / 53HK-L / longcat 只做密钥替换，模型与授权原样保留）；
  用户给的中转站账号密码不落任何持久化位置。
- 验证口径：新增单测 1 例（内存 SQLite 直查行）；活体复验（create 传 enabled:false 后库里为 0）；
  `python scripts/api-tests/run_all.py` 19 套件；`go test ./... -count=1`。
- 状态：已完成（见 ledger 本行备注与 worklog/2026-09-16-渠道凭据补录与凭据启用位修复.md）。
- 未决（需用户拍板，未擅自实施）：单成员分组在成员冷却期内静默等待是否改为快速失败；
  两站点登录自动化被人机验证拦下，是否授权重试或用「手动登录跳转页」。
