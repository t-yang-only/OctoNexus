# 认领：T-pool-002 预研草稿（NM-CUR-204，L8 调度：预研 only，不动代码）

- 认领 Agent：cursor-local（NM-CUR-204）
- 认领时间：2026-09-14
- 登记台：`docs/agents/ledger.md` T-pool-002（todo，无认领）
- 调度依据：NM-CUR-201 L8——"预研 only（官方账号→渠道轮询转发方案草稿
  `docs/requirements/2026-09-14-pool-002预研.md`），不开工实现，不动代码"；
  实施门未过（W1#3=T-acct-001 doing、W2#2=T-route-002 done、T-proto-001 done），
  预研不依赖门禁。
- 来源需求：R-pool-002（用户 2026-09-13 原话：Google/ChatGPT 官方账号直接当号池做转发，
  轮询+均衡请求+协议转化）。
- 范围：
  1. 形态假设：官方账号如何映射到现有 Channel/ChannelKey/ChannelModel/ChannelGrant 四层模型；
  2. 轮询/均衡：与 T-route-002 `internal/relay/balance.go`（加权轮询第一版，未接热路径）的
     接入点与缺口；
  3. 协议转化：T-proto-001 矩阵（proto_matrix_test.go 6 例）对官方账号↔三协议的覆盖结论引用；
  4. 余额/停链路：官方账号的额度可见性差异（套餐制 vs 点数制）→ T-quota-001 采集口径适配；
  5. 合规红线：仅官方 OAuth/扫码（T-acct-001 口径），禁验证码绕过/逆向/cookie 盗用；
     凭据密文落库（AccessCipher），日志无明文。
  6. 前置依赖与风险：T-acct-001（授权接入）未完成前，号池无凭据可填——预研只定接口形状，
     不抢实施。
- 红线：纯文档；不写业务代码、不改 model/op/relay/health；不落任何真实凭据。
- 验收：`docs/requirements/2026-09-14-pool-002预研.md` 一页以上、四节（形态/接入点/口径/
  风险）成文、引用既有模块路径；登记表 204 翻已完成；T-pool-002 维持 todo 不关行
  （实施门未过，预研≠完成）。
