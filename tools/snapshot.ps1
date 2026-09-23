# 生成源码快照：只含 git 跟踪的文件。
#
# 为什么必须用 git archive 而不是 Copy-Item -Recurse：
# 工作目录里躺着大量被 .gitignore 排除的产物 —— octopus.exe(70MB)、
# scripts/api-tests/requests.jsonl(实测 471MB 的测试录制)、reference/(参考源码树)。
# 直接复制目录会把它们一并打进去：2026-09-23 11:46 那个快照因此膨胀到 764MB，
# 而同一个仓库的干净快照只有 11.6MB。快照的价值是「源码可回溯」，不是「目录镜像」，
# 备份里混进 471MB 测试产物只会让归档失真、拖慢复制与上传。
#
# 用法：
#   pwsh -File tools/snapshot.ps1                 # 快照当前 HEAD
#   pwsh -File tools/snapshot.ps1 -Ref v0.58.0    # 快照指定 tag/commit
#   pwsh -File tools/snapshot.ps1 -Keep 20        # 快照后只保留最近 20 份
[CmdletBinding()]
param(
    [string]$Ref = "HEAD",
    [string]$Root = "D:\奇怪的软件\octopus-本地数据",
    [int]$Keep = 0
)

$ErrorActionPreference = "Stop"
$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

# 提交号必须钉死在快照名里 —— 稀疏检出/换分支后，光凭目录名无法知道它对应哪次提交。
$commit = (git rev-parse --short $Ref).Trim()
if (-not $commit) { throw "无法解析 ref: $Ref" }
$status = git status --porcelain
if ($status) {
    Write-Warning "工作区有未提交改动，快照只包含已提交内容（$commit）："
    $status | Select-Object -First 10 | ForEach-Object { Write-Warning "  $_" }
}

$stamp = Get-Date -Format "yyyyMMdd-HHmm"
$dst = Join-Path $Root "源码快照-$stamp-$commit"
if (Test-Path $dst) { Remove-Item $dst -Recurse -Force }

$zip = Join-Path $env:TEMP "octopus-snapshot-$stamp.zip"
git archive --format=zip -o $zip $Ref
Expand-Archive -Path $zip -DestinationPath $dst
Remove-Item $zip -Force

$files = Get-ChildItem $dst -Recurse -File
$mb = [math]::Round(($files | Measure-Object Length -Sum).Sum / 1MB, 2)

# 自检：快照里不该出现被 git 忽略的产物。出现即说明实现被改回了目录复制。
$junk = @("octopus.exe") | Where-Object { Test-Path (Join-Path $dst $_) }
if ($junk) { throw "快照被污染（含被忽略产物：$($junk -join ', ')）：$dst" }

Write-Output "快照完成: $dst"
Write-Output "  提交 $commit | 文件 $($files.Count) | 大小 $mb MB"

if ($Keep -gt 0) {
    $all = Get-ChildItem $Root -Directory -Filter "源码快照-*" | Sort-Object Name
    if ($all.Count -gt $Keep) {
        $all | Select-Object -First ($all.Count - $Keep) | ForEach-Object {
            Remove-Item $_.FullName -Recurse -Force
            Write-Output "  清理旧快照: $($_.Name)"
        }
    }
}
