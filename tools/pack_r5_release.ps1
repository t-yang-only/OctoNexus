$ErrorActionPreference = "Stop"
$rel = "D:\奇怪的软件\octopus-本地数据\release-v0.79.42"
New-Item -ItemType Directory -Path $rel -Force | Out-Null
Copy-Item "D:\奇怪的软件\octopus\octopus-linux-amd64" $rel -Force
$hash = (Get-FileHash "$rel\octopus-linux-amd64" -Algorithm SHA256).Hash.ToLower()
$size = (Get-Item "$rel\octopus-linux-amd64").Length
"$hash  octopus-linux-amd64" | Set-Content "$rel\SHA256SUMS.txt" -Encoding ASCII
@"
version: v0.79.42
commit: 4da4b6a
built_at: 2026-10-23T22:52:39Z
size: $size
sha256: $hash
scope: Round 5 Sub2API 动态模型同步三项缺陷闭环（R5-F1 并发互斥/R5-F2 凭据名跟随/R5-F3 无凭据可执行报错）
frontend: 无改动（static/out 与 v0.79.41 一致）
verify: 上传后 cd release 目录 sha256sum -c SHA256SUMS.txt；服务器 /opt/octonexus-deta/octopus version 看 Version 与 Commit ID
"@ | Set-Content "$rel\build-record.txt" -Encoding UTF8
Get-ChildItem $rel | Select-Object Name, Length
