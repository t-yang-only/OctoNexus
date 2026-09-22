#!/usr/bin/env bash
# 清理误提交进历史的 octopus.exe~（70MB Go 构建残留）
#
# 背景：8a9b12a 误把 70MB 的构建残留提交进去，b2be0d8 才删除。
# 删除文件不会删掉历史里的 blob —— 它仍在 8a9b12a 里，占仓库约 40MB（压缩后）。
#
# 影响范围（已核实）：只有 8a9b12a 与 b2be0d8 两个提交含它，
# 且这两个提交刚推上去、没有其他人在用这个仓库拉过（origin 是你自己的 fork）。
#
# ⚠️ 这会**重写已推送的历史**并需要 force push。
#    如果已经有别人克隆了这个仓库，他们的本地历史会与远端分叉，
#    需要他们重新 clone 或执行 git reset --hard origin/master。
#    只有你确认没人依赖这两个提交时才执行。
#
# 用法：
#   bash tools/clean-big-blob.sh            # 演练：只报告会做什么
#   bash tools/clean-big-blob.sh --apply    # 真执行（会 force push）
set -euo pipefail

APPLY=0
[ "${1:-}" = "--apply" ] && APPLY=1

TARGET="octopus.exe~"
BRANCH="master"

cd "$(dirname "$0")/.."

echo "== 1. 现状 =="
echo "当前分支: $(git rev-parse --abbrev-ref HEAD)"
echo "HEAD: $(git rev-parse --short HEAD)"
echo "含该文件的提交:"
git log --oneline --all -- "$TARGET" | sed 's/^/  /'
echo "该文件的字节数: $(git cat-file -s "$(git rev-parse "HEAD^:$TARGET" 2>/dev/null || echo HEAD)" 2>/dev/null || echo '?')"

if [ "$APPLY" = "0" ]; then
    echo
    echo "== 2. 将执行的操作（演练，未改动任何东西）=="
    echo "  a) 用 git filter-branch 从所有提交里移除 $TARGET"
    echo "  b) git reflog expire + git gc --prune=now 回收空间"
    echo "  c) git push --force-with-lease origin $BRANCH"
    echo
    echo "确认无其他人依赖这两个提交后，用 --apply 真执行。"
    exit 0
fi

echo
echo "== 2. 前置检查 =="
if [ -n "$(git status --porcelain)" ]; then
    echo "FAIL  工作区不干净，先提交或 stash 再执行" >&2
    exit 1
fi
echo "PASS  工作区干净"

# 记录清理前的分叉点，便于回滚
BACKUP_REF="backup-before-clean-$(date +%Y%m%d-%H%M%S)"
git branch "$BACKUP_REF"
echo "PASS  已建备份分支 $BACKUP_REF（出问题可用它恢复）"

echo
echo "== 3. 从历史移除 $TARGET =="
FILTER_BRANCH_SQUELCH_WARNING=1 git filter-branch --force --index-filter \
    "git rm --cached --ignore-unmatch '$TARGET'" \
    --prune-empty --tag-name-filter cat -- --all

echo
echo "== 4. 回收空间 =="
rm -rf .git/refs/original/
git reflog expire --expire=now --all
git gc --prune=now --aggressive

echo
echo "== 5. 校验 =="
left=$(git log --oneline --all -- "$TARGET" | wc -l)
if [ "$left" != "0" ]; then
    echo "FAIL  仍有 $left 个提交引用该文件" >&2
    exit 1
fi
echo "PASS  历史里已无该文件"
echo "INFO  当前 .git 体积: $(du -sh .git | cut -f1)"
if [ -e "$TARGET" ]; then
    echo "FAIL  工作区又出现了 $TARGET —— 拒绝推送（否则等于把它再提交一次）" >&2
    exit 1
fi
echo "PASS  工作区没有该文件"

echo
echo "== 6. 推送到远端（force，带 lease 保护）=="
git push --force-with-lease origin "$BRANCH"

echo
echo "完成。备份分支 $BACKUP_REF 保留在本地，确认无误后可用："
echo "  git branch -D $BACKUP_REF"
