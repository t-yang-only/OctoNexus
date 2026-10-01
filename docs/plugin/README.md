# 反代扩展插件（R-plugin-001）

把"某个账号池 → OpenAI 兼容接口"这件事开放给社区：**你自己写反代工具，octopus 负责它的运行环境、
出网出口与接入主链路。** 本文件是插件作者需要的全部契约。

---

## 1. 三句话说完

1. 把工具丢进 `<数据目录>/plugins/<你的插件名>/`，写一份 `plugin.json`，重启或点一次「重新扫描」。
2. octopus 为它分配一个本地端口、拉起进程，并把**出口代理**注入环境变量 —— 你的工具不需要写任何代理代码。
3. 它同时被注册成一个**普通渠道**（`http://127.0.0.1:<端口>`），于是分压、监控、选路、余额、失败记账全部生效。

## 2. 目录结构

```
<数据目录>/plugins/my-relay/
├── plugin.json      # 必需：清单
├── my-relay.exe     # 你的工具（或 .py/.js/…，见下）
└── plugin.log       # octopus 自动生成：你的 stdout/stderr
```

- `<数据目录>` 就是 octopus 的 `data/`（与 `data.db` 同级）。
- 目录名即插件标识（slug），只允许小写字母、数字与连字符。
- 清单里的 `slug`（若写）必须与目录名一致 —— 防止一份清单被复制到别处冒充另一个插件。
- **入口必须是插件目录内的文件**：绝对路径、`../` 逃逸一律拒绝装载。

## 3. `plugin.json` 字段

| 字段 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- |
| `name` | 是 | — | 展示名 |
| `version` | 否 | 空 | 你自报的版本，只作展示 |
| `runtime` | 否 | `exec` | `exec`=octopus 拉起进程；`http`=指向已存在的服务 |
| `entry` | 是 | — | `exec`：相对插件目录的入口文件；`http`：服务地址（如 `http://127.0.0.1:8080`） |
| `args` | 否 | `[]` | 启动参数，支持占位符 `{port}` `{dir}` `{data}` `{slug}` |
| `port_env` | 否 | `PORT` | octopus 用哪个环境变量告诉你要监听的端口 |
| `protocol` | 否 | `openai` | `openai` / `anthropic`，仅作展示与接入提示 |
| `base_path` | 否 | `/v1` | 你的接口前缀（面板/文档会引用它） |
| `health_path` | 否 | `<base_path>/models` | 就绪探测路径（octopus 目前按 TCP 端口判就绪，此字段作提示） |
| `egress` | 否 | `pool` | `pool`=出网强制走节点池出口；`direct`=显式声明允许直连真实 IP；`http` 运行时用 `external` |
| `auto_channel` | 否 | `true` | 启动后自动注册/更新为渠道 |
| `auto_start` | 否 | `false` | 随 octopus 启动自动拉起 |

最小可用清单：

```json
{
  "name": "我的反代",
  "version": "1.0.0",
  "entry": "my-relay.exe",
  "protocol": "openai"
}
```

## 4. octopus 注入给你的环境变量（契约）

| 变量 | 含义 |
| --- | --- |
| `PORT`（或你声明的 `port_env`） | **必须监听的端口**（`127.0.0.1`） |
| `OCTOPUS_PLUGIN_PORT` | 同上（固定名，便于排障） |
| `OCTOPUS_PLUGIN_SLUG` / `OCTOPUS_PLUGIN_DIR` / `OCTOPUS_DATA_DIR` | 标识与路径 |
| `OCTOPUS_EGRESS_PROXY` | **出口代理**：`http://127.0.0.1:<内核入站端口>` |
| `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY`（及小写形态） | 同上（大多数运行时/HTTP 客户端默认就认这几个，**你什么都不用写**） |
| `NO_PROXY=127.0.0.1,localhost,::1` | 回环不走代理（你回调 octopus 或访问自己时要靠它） |
| `OCTOPUS_PLUGIN_TOKEN` | 共享令牌：octopus 调你时会带 `Authorization: Bearer <它>`，建议校验 |
| `OCTOPUS_ADMIN_BASE` / `OCTOPUS_RELAY_BASE` | octopus 管理面/转发口的回环地址（要回读配置时用） |

**出口是硬承诺**：清单声明 `egress=pool`（默认）时，octopus 在拿到可用节点出口前**不会启动你**，
也绝不"注入不了就直连" —— 那会让上游看到你的真实 IP。没配出口时的报错会直接说明怎么配。

## 5. 接口约定

- 监听 `127.0.0.1:$PORT`，提供 OpenAI 兼容路径（如 `/v1/models`、`/v1/chat/completions`）。
- 请求带 `Authorization: Bearer $OCTOPUS_PLUGIN_TOKEN`（自动注册的渠道凭据就是它）。
- 流式请用 SSE（`text/event-stream`），octopus 支持转发。
- 校验令牌失败返回 401 即可，不要把内容当成功返回。

## 6. 运行时行为（你需要知道的三件事）

1. **进程托管**：octopus 结束你时会连子进程一起收（`taskkill /T`；类 Unix 上是进程组），所以别把状态只放在内存里 —— 退出即丢。
2. **端口稳定**：端口一旦分配就不再漂移（除非你改了 `runtime`/`entry`/`port_env`）。渠道地址因此不会变。
3. **崩溃可见**：你退出后状态会被记成 `exited`（含退出码与日志尾巴），面板不会假装你还在跑。

## 7. 管理接口（面板/脚本都用它）

全部挂在管理面（`middleware.Auth()`，即登录后的 Cookie），**转发口不暴露插件管理**：

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/v1/plugin/list` | 插件列表（含运行状态、端口、出口、日志尾部） |
| POST | `/api/v1/plugin/scan` | 重新扫描目录（新拷贝进来的工具立即可见） |
| GET | `/api/v1/plugin/{slug}` | 单个插件状态 |
| PUT | `/api/v1/plugin/{slug}` | 改运行期选定项：`enabled` / `auto_start` / `egress_node_id` |
| POST | `/api/v1/plugin/{slug}/start` | 启动（含分配端口、注入出口、注册渠道） |
| POST | `/api/v1/plugin/{slug}/stop` | 停止 |
| DELETE | `/api/v1/plugin/{slug}` | 删除记录（不删目录、不删你的文件） |

相关设置项：`plugin_port_start` / `plugin_port_end`（默认 42000-42999）、
`plugin_default_egress_id`（未单独指定出口的插件用哪个节点；0 表示没有默认出口）、
`plugin_http_hosts`（`runtime=http` 允许连的远端主机白名单，默认空 = 只允许本机回环）。

## 8. 一个可跑的最小例子

见自检桩 `stub-relay`（本轮活体验证用的就是它）：监听 `$PORT`、`/v1/models` 返回模型列表、
用环境里的代理出网回报出口 IP。验证结论（本地实例实测）：插件出网与节点探活看到的是**同一个出口 IP**，
即"插件没写一行代理代码，出口却已经是节点"。

## 9. 边界与诚实说明

- `runtime=http` 指向的是**已经跑着的服务**：octopus 无法为它注入出口，它的出网由你自己负责 ——
  要受管控就用 `runtime=exec` 把工具交给 octopus 托管。
- 插件令牌经环境变量注入（这是进程间传递凭据的唯一实用方式），因此**同机器上能读你进程环境的人可以拿到它**；
  与"把凭据写进命令行参数"相比这已是更安全的做法，但它不是机密隔离边界。
- `egress=direct` 只在你显式声明时生效，且会在状态里持续提示"直连真实出口、可能被上游关联"。
