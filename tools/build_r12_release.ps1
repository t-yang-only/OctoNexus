$ErrorActionPreference = "Stop"
Set-Location "D:\奇怪的软件\octopus"
$ts = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$ld = "-s -w -X github.com/t-yang-only/OctoNexus/internal/conf.Version=v0.79.48 -X github.com/t-yang-only/OctoNexus/internal/conf.BuildTime=$ts -X github.com/t-yang-only/OctoNexus/internal/conf.Commit=$(git rev-parse --short HEAD)"
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
go build -trimpath -tags=jsoniter -ldflags $ld -o octopus-linux-amd64 main.go
if ($LASTEXITCODE -ne 0) { Write-Output "BUILD_FAILED exit=$LASTEXITCODE"; exit 1 }
$rel = "D:\奇怪的软件\octopus-本地数据\release-v0.79.48"
New-Item -ItemType Directory -Path $rel -Force | Out-Null
Copy-Item "octopus-linux-amd64" $rel -Force
$hash = (Get-FileHash "$rel\octopus-linux-amd64" -Algorithm SHA256).Hash.ToLower()
$size = (Get-Item "$rel\octopus-linux-amd64").Length
"$hash  octopus-linux-amd64" | Set-Content "$rel\SHA256SUMS.txt" -Encoding ASCII
@"
version: v0.79.48
commit: $(git rev-parse --short HEAD)
built_at: $ts
size: $size
sha256: $hash
scope: Round 12 停用账号三入口守卫（probe/sync/clean-stale 一致拒绝 + 前端按钮隐藏）
frontend: 已改动并 pnpm build（新资产 index-Dml3c1cV.js）
verify: 上传后 sha256sum -c；服务器 version；匿名 401；域名首页应引用 index-Dml3c1cV.js；真实 Key 转发 200
"@ | Set-Content "$rel\build-record.txt" -Encoding UTF8
Write-Output "BUILD_OK ts=$ts size=$size sha256=$hash"
