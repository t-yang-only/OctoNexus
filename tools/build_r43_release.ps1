$ErrorActionPreference = "Stop"
Set-Location "D:\奇怪的软件\octopus"
$ts = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$commit = $(git rev-parse --short HEAD)
$ld = "-s -w -X github.com/t-yang-only/OctoNexus/internal/conf.Version=v0.79.80 -X github.com/t-yang-only/OctoNexus/internal/conf.BuildTime=$ts -X github.com/t-yang-only/OctoNexus/internal/conf.Commit=$commit"
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
go build -trimpath -tags=jsoniter -ldflags $ld -o octopus-linux-amd64 main.go
if ($LASTEXITCODE -ne 0) { Write-Output "BUILD_FAILED exit=$LASTEXITCODE"; exit 1 }
$rel = "D:\奇怪的软件\octopus-本地数据\release-v0.79.80"
New-Item -ItemType Directory -Path $rel -Force | Out-Null
Copy-Item "octopus-linux-amd64" $rel -Force
$hash = (Get-FileHash "$rel\octopus-linux-amd64" -Algorithm SHA256).Hash.ToLower()
$size = (Get-Item "$rel\octopus-linux-amd64").Length
"$hash  octopus-linux-amd64" | Set-Content "$rel\SHA256SUMS.txt" -Encoding ASCII
@"
version: v0.79.80
commit: $commit
built_at: $ts
size: $size
sha256: $hash
scope: Round 43 出站与终态响应头透传 X-Octopus-Request-Id 实现端到端全链路可观测（在 publishDecision、failRequest 中均注入 X-Octopus-Request-Id 关联客户端请求与服务端审计日志）
frontend: 无改动（沿用 v0.79.50/51/52/53/54/55/56/57/58/59/60/61/62/63/64/65/66/67/68/69/70/71/72/73/74/75/76/77/78/79 资产 index-JM8xRsR6.js）
verify: 上传后 sha256sum -c；服务器 version；匿名 401；真实 Key 转发 200 并检查响应头 X-Octopus-Request-Id；DB 完整
"@ | Set-Content "$rel\build-record.txt" -Encoding UTF8
Write-Output "BUILD_OK ts=$ts size=$size sha256=$hash"
