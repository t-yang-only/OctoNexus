#!/usr/bin/env bash
# 回滚点清理逻辑的本地演练：验证 keep 的三种取值都不会删错。
# 用法：bash docs/deploy/prune-artifacts-drill.sh
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SOURCE="$SCRIPT_DIR/upgrade-on-server.sh"
[ -f "$SOURCE" ] || { echo "找不到 $SOURCE"; exit 1; }

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

mkdir -p "$T/backups"
for i in 1 2 3 4 5 6 7; do
    mkdir -p "$T/backups/2026092$i-120000"
    echo "x" >"$T/backups/2026092$i-120000/data.tar.gz"
    sleep 0.01
done
echo "造了 $(ls -1d "$T"/backups/*/ | wc -l) 个回滚点"

# 抽出 prune 函数单独测（避免跑整个升级脚本）
{
    echo 'set -eu'
    echo 'DIR="$1"; KEEP_ARTIFACTS="$2"'
    echo 'log()  { printf "%s\n" "$*"; }'
    echo 'ok()   { printf "PASS  %s\n" "$*"; }'
    echo 'warn() { printf "WARN  %s\n" "$*"; }'
    sed -n '/^prune_old_artifacts() {/,/^}/p' "$SOURCE"
    echo 'prune_old_artifacts'
} >"$T/run.sh"

run_case() {
    local keep="$1" label="$2"
    echo "--- $label (keep=$keep) ---"
    bash "$T/run.sh" "$T" "$keep" 2>&1 | sed 's/^/  /'
    echo "  剩余: $(ls -1d "$T"/backups/*/ 2>/dev/null | wc -l) 个"
}

run_case 3 "保留最近 3 个"
run_case 0 "keep=0 应夹回 1，不能删光"
run_case abc "非数字应回落默认 3"
run_case 1 "只留 1 个"

# 判据：最后必须恰好剩 1 个（keep=1）
left=$(ls -1d "$T"/backups/*/ 2>/dev/null | wc -l)
echo
if [ "$left" -eq 1 ]; then
    echo "PASS  清理逻辑符合预期（keep=1 后恰好剩 1 个）"
else
    echo "FAIL  keep=1 后应剩 1 个，实得 $left 个"
    exit 1
fi
