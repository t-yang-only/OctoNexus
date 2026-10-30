import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.81] - 2026-10-30
### Network & Reliability
- **渠道级专用代理加固与跨环境网络韧性对齐 (Round 44)**：
  - **R44-F1 (HIGH) apigoto 等跨地域抖动渠道绑定 channel_proxy 消除 TLS/首帧超时**：由于部分上游机房（如 api.apigoto.com 45.124.77.55）与海外云节点之间偶发丢包与 TLS 1.3 握手阻塞，导致出现 `TLS handshake timeout` 与 `stream first event timeout`；现将正式版与测试版平台全部对齐渠道级独立代理机制（`proxy=1, channel_proxy='socks5://127.0.0.1:7890'`），实现针对特定服务商的低延迟隧道出站，测试版（apidate.yangonly.com）端到端实测首帧 <300ms 畅通。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：3 / 10 (第五周期 Round 43)', '- 当前周期累计已完成轮次：4 / 10 (第五周期 Round 44)', 1)
anchor = '- Round 43（响应头透传 X-Octopus-Request-Id 端到端可观测'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r44 = '''
- Round 44（渠道专用代理与网络韧性双环境对齐，v0.79.81 已同步）：apigoto（ID=28）绑定 channel_proxy='socks5://127.0.0.1:7890'（R44-F1）；测试版（apidate.yangonly.com）实测非流式与流式双双 200，首帧 <300ms，X-Octopus-Request-Id 正确透传，DB 21/60/sealed 60 无损'''
s2 = s2[:eol] + r44 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_R44_UPDATED')
