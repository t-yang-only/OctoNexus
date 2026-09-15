# octopus 本地 API 测试套件

一条命令跑完本地 API 测试矩阵。原本这些脚本散在 `%TEMP%` 里、每次都要手工按顺序跑，
容易漏项也难以复现；现在收进仓库，作为改动后的固定验收手段（NM-DS-009「搭建测试」）。

## 快速开始

```bash
# 1) 起本地实例（默认 admin 3303 落在本机 Windows 排除端口段 3241-3340，必须覆盖端口）
cd <repo>
OCTOPUS_SERVER_ADMIN_PORT=13303 OCTOPUS_SERVER_RELAY_PORT=11234 ./octopus.exe start

# 2) 跑全部套件（自动拉起 mock 上游、自动编译日志卡片纯函数、自动导出真实日志样本）
python scripts/api-tests/run_all.py

# 只跑某几项 / 看清单
python scripts/api-tests/run_all.py --only format,failover,stats
python scripts/api-tests/run_all.py --list
```

退出码：`0` 全部通过；`1` 有套件未通过；`2` 前置不满足（实例没起、mock 起不来、套件名写错）。

## 套件

| 键 | 覆盖 |
|----|------|
| `entities` | 测试实体与分组（幂等：渠道 `DS-TEST-mock` + 各 `DS-TEST-*` 分组） |
| `format` | 多格式调用矩阵：三协议（Chat/Responses/Messages）× 流式/非流式，含跨协议转换、鉴权边界、未知模型 |
| `lossless` | 跨协议无损转化：3 客户端协议 × 3 上游协议钉死，标记/工具/max_tokens/temperature 逐项校验 |
| `real` | 真实上游调用：成本与价表逐项对照 |
| `failover` | 超时切换与故障转移：失败重试到上限、冷却与跳过、非流式整响应超时、流式首事件超时、全员不可用 |
| `probe` | 主动探活：上游恢复后提前解除冷却、流量回切、探测是最小真实请求（`max_tokens=1`）、探测失败不改冷却且不重试、设置热写热读并复原（**会在套件内临时打开 `route_probe_enabled`，`finally` 复原**） |
| `notify` | 多渠道通知：四家报文形状（通用 webhook / 飞书 / 钉钉 / 企微）、SMTP 真投递（套件内起 SMTP sink）、「HTTP 200 + 业务错误码」判失败、单渠道失败不影响其它渠道、未配置渠道报缺项、真实探活事件经 fan-out 抵达 webhook；**套件内临时改写全部通知设置，`finally` 原样复原** |
| `export` | 请求级明细导出：CSV 的 BOM/表头/行序与唯一行 ID、与 `/history` 字段级等价、状态/模型/关键字筛选、下载响应头、未登录 401 |
| `stats` | 后台统计审计：日志行字段、缓存自洽、daily 收敛、usage 与 `relay_logs` 对照、按渠道计数 |
| `apikey` | Key 级审计：新 Key 转发、自助/管理端统计一致、RPM/TPM 限流与 `Retry-After`、过期/禁用/超额/伪造/越权、SSE 概览、人工中止轮次、Key 登录 |
| `costmode` / `quality` / `latency` / `busy` / `rpm` | 五种路由策略的活体验证（最低成本 / 质量优先 / 最低延迟 / 最空闲 / 近期消耗最低）。其中 `rpm` 会**删组重建**以获得干净的成员行，并用 mock 的 `usage_scale` 把某个成员的 token 放大 40 倍，验证"token 优先于请求数" |
| `hotapply` | 设置热生效（改完不重启即生效） |
| `display` | 日志卡片字段容错解析（把 `web/src/.../log/display.ts` 单独编译成 cjs 后跑纯函数断言） |
| `reallog` | 日志卡片真实数据等价性（用库里最近两条 `relay_logs` 对照新解析与旧内联公式） |

## 约定

- **测试脚手架不落凭据**：`mock_upstream.py` 把上游请求里的 `Authorization` / `x-api-key` 换成
  `bearer:<sha256 前 12 位>` 指纹再写日志，只够区分"用了哪把凭据"，不泄露内容。
- **运行期产物不入库**：`requests.jsonl` / `real_logs.json` / `display.cjs` / `mock.log` 由本目录的
  `.gitignore` 排除。
- **端口可覆盖**：`OCTOPUS_ADMIN_URL`（默认 `http://127.0.0.1:13303`）、`OCTOPUS_RELAY_HOST`、
  `OCTOPUS_RELAY_PORT`（11234）、`OCTOPUS_MOCK_BASE`（`http://127.0.0.1:18099/v1`）、
  `OCTOPUS_MOCK_PORT`（18099）、`OCTOPUS_DB`（默认 `<repo>/data/data.db`）、`MOCK_SLOW_SECONDS`（15）。
- **mock 上游行为**：模型名含 `slow` → 先 sleep `MOCK_SLOW_SECONDS` 再应答（驱动超时切换）；
  含 `bad` → 返回 500（驱动重试/冷却）；`mock-good` 正常；三协议（`/v1/chat/completions`、
  `/v1/responses`、`/v1/messages`）都支持，另有 `/v1/models` 供拉取模型与探针使用。
  运行期可用 `POST /__control {"model":"mock-bad","behavior":"ok|bad|slow|clear"}` 覆盖某个模型的行为
  （`GET /__control` 看当前覆盖）——探活用例要靠它把"上游恢复"造出来，因为成员模型名是落库配置、改不了名。
  另有通知渠道桩：`POST /notify/echo|feishu|dingtalk|wecom` 返回各家形状的成功应答，
  `/notify/feishu-fail` 返回"HTTP 200 + 业务错误码"，用于验证发送方真的在检查响应体。
- **真实上游套件**依赖库里存在可用的真实分组（本机默认用 `deepseek-v4-flash`）；
  没有真实渠道时该套件会失败，其余套件不受影响。

## 加一个新套件

1. 在 `scripts/api-tests/` 放脚本，末尾打印形如 `NAME total=4 pass=4 fail=0` 的汇总行（运行器按此行汇总）；
2. 在 `run_all.py` 的 `SUITES` 里登记（键、脚本名、说明、依赖：`instance` / `mock` / `db` / `node` / `esbuild`）。

## 已知限制

- `failover` / `latency` / `busy` 依赖 mock 的 15s 慢响应，`probe` 依赖两个探活周期，整轮实测约 4-6 分钟；
- 需要 mock 的套件在 `--only` 选中时才会拉起 mock；单独跑某个脚本前请先确保 mock 在 18099；
- 本套件验证的是**本机实例的真实 HTTP 行为**，不做单元测试替代：Go 单测仍走 `go test ./...`。
