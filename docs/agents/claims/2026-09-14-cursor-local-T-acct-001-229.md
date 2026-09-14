# 认领：T-acct-001 官方账号 OAuth 接入收口（NM-CUR-228/229）

- 认领 Agent：cursor-local（NM-CUR-228）
- 收口会话：NM-CUR-229
- 范围：163 五步全部补齐（三接口、PKCE、AutoMigrate、密文、单测），不抢其他车道代码。
- 实现：`internal/model/official_account.go`、`internal/op/official_account.go`、`internal/server/handlers/official_account.go`
- 验证：官方账号流程 7 例全绿；全量 build/vet/test、tsc、pnpm build 绿
- 收口 commit：`1e644b1`、`71c12e7`
- 状态：`T-acct-001 → done`
