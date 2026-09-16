#!/usr/bin/env python3
"""看一眼备份文件里到底有什么：结构、计数、以及能不能拿来导入（只读，不改任何库）。

用途：拿到一份 octopus 导出的备份（`/api/v1/setting/export` 的产物）时，先做一次**只读体检**：
  - 顶层结构是否齐全（version / exported_at + 九张表）；
  - 每张表的条数（与预期对不上就能立刻发现"备份不完整"）；
  - 参照完整性：渠道模型指向的渠道、授权指向的渠道/模型、分组成员指向的分组，是否都存在；
  - 分组口径：分组名是否重复、每组的成员数与协议位是否自洽；
  - **凭据面**：本工具只报"有没有、多少条"，永远不打印凭据值本身。

用法：
  python verify_backup.py [--backup <备份 json 路径>] [--expect-channels 12]

退出码：0 体检通过；1 有硬性问题（缺表/参照断裂/分组重名）；2 文件读不了或不是 JSON。
"""

import argparse
import io
import json
import os
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_BACKUP = os.path.normpath(
    os.path.join(HERE, "..", "..", "..", "octopus-本地数据", "数据备份(勿提交).json"))

TABLES = [
    ("channels", "渠道"),
    ("channel_keys", "渠道凭据"),
    ("channel_models", "渠道模型"),
    ("channel_grants", "渠道授权"),
    ("groups", "分组"),
    ("group_items", "分组成员"),
    ("llm_infos", "模型价格"),
    ("api_keys", "API Key"),
    ("settings", "系统设置"),
]


def load(path):
    with io.open(path, "r", encoding="utf-8", errors="replace") as fh:
        return json.loads(fh.read())


def ids(rows, field="id"):
    return {str(row.get(field)) for row in rows if isinstance(row, dict) and row.get(field) is not None}


def main():
    parser = argparse.ArgumentParser(description="备份文件只读体检")
    parser.add_argument("--backup", default="", help="备份 json 路径（默认取仓库旁的本地数据目录）")
    parser.add_argument("--expect-channels", default="0", help="预期渠道数，给了就一起校验")
    args = parser.parse_args()

    path = args.backup or DEFAULT_BACKUP
    if not os.path.exists(path):
        print("找不到备份文件：%s" % path)
        return 2
    try:
        dump = load(path)
    except ValueError as exc:
        print("不是合法 JSON：%s" % exc)
        return 2

    problems = []
    print("备份：%s（%.1f MB）" % (path, os.path.getsize(path) / 1048576.0))
    print("导出版本=%s 导出时间=%s" % (dump.get("version"), dump.get("exported_at")))

    counts = {}
    print("\n-- 表计数 --")
    for key, label in TABLES:
        rows = dump.get(key)
        if rows is None:
            problems.append("缺表 %s（%s）" % (key, label))
            print("%-16s 缺失" % key)
            continue
        counts[key] = len(rows)
        print("%-16s %5d  条（%s）" % (key, len(rows), label))

    expected = int(args.expect_channels or 0)
    if expected and counts.get("channels") != expected:
        problems.append("渠道数 %s ≠ 预期 %d" % (counts.get("channels"), expected))

    print("\n-- 参照完整性 --")
    channel_ids = ids(dump.get("channels") or [])
    model_ids = ids(dump.get("channel_models") or [])
    grant_ids = ids(dump.get("channel_grants") or [])
    group_ids = ids(dump.get("groups") or [])

    def orfan(rows, field, known, label):
        missing = set()
        for row in rows or []:
            value = row.get(field)
            if value is None:
                continue
            if str(value) not in known:
                missing.add(str(value))
        if missing:
            problems.append("%s 有 %d 个指向不存在的对象（例：%s）" % (label, len(missing), sorted(missing)[:3]))
        print("%-26s 断裂 %d" % (label, len(missing)))

    orfan(dump.get("channel_models"), "channel_id", channel_ids, "渠道模型 → 渠道")
    orfan(dump.get("channel_grants"), "channel_id", channel_ids, "授权 → 渠道")
    orfan(dump.get("channel_grants"), "channel_model_id", model_ids, "授权 → 渠道模型")
    orfan(dump.get("group_items"), "group_id", group_ids, "分组成员 → 分组")
    orfan(dump.get("group_items"), "channel_grant_id", grant_ids, "分组成员 → 授权")

    print("\n-- 分组口径 --")
    names = {}
    for group in dump.get("groups") or []:
        name = (group.get("name") or "").strip()
        names[name] = names.get(name, 0) + 1
    duplicated = sorted(name for name, count in names.items() if count > 1)
    if duplicated:
        problems.append("分组重名 %d 个（例：%s）" % (len(duplicated), duplicated[:3]))
    modes = {}
    for group in dump.get("groups") or []:
        mode = group.get("mode") or "(空)"
        modes[mode] = modes.get(mode, 0) + 1
    members_per_group = {}
    for item in dump.get("group_items") or []:
        key = str(item.get("group_id"))
        members_per_group[key] = members_per_group.get(key, 0) + 1
    lonely = [gid for gid in group_ids if not members_per_group.get(gid)]
    print("分组 %d 个，重名 %d 个，成员数为 0 的分组 %d 个" % (len(group_ids), len(duplicated), len(lonely)))
    print("分组成员数：最少 %d / 最多 %d" % (
        min(members_per_group.values()) if members_per_group else 0,
        max(members_per_group.values()) if members_per_group else 0))
    print("选路模式分布：%s" % json.dumps(modes, ensure_ascii=False))
    if lonely:
        problems.append("有 %d 个分组没有任何成员（例：%s）" % (len(lonely), lonely[:3]))

    print("\n-- 凭据面（只报个数，不打印内容）--")
    keys = dump.get("channel_keys") or []
    with_key = [row for row in keys if row.get("key")]
    print("渠道凭据 %d 条，其中带 key 值 %d 条（内容不打印）" % (len(keys), len(with_key)))
    apikeys = dump.get("api_keys") or []
    print("API Key %d 条（内容不打印），其中启用 %d 条" % (
        len(apikeys), len([row for row in apikeys if row.get("enabled")])))
    if not with_key and keys:
        problems.append("渠道凭据一条 key 值都没有：备份可能被脱敏过（导入后渠道会不可用）")

    print("\n================ 体检结论 ================")
    if problems:
        for problem in problems:
            print("问题：%s" % problem)
        print("VERIFY_BACKUP 有 %d 个问题" % len(problems))
        return 1
    print("VERIFY_BACKUP 通过：结构齐全、参照完整、分组口径自洽")
    return 0


if __name__ == "__main__":
    sys.exit(main())
