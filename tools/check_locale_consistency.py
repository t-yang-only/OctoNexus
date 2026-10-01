# -*- coding: utf-8 -*-
"""三语 locale 一致性判据。

# 为什么需要这个检查

漏译/错译的表现不是报错，而是界面上出现 `log.filter.from` 这样的键名或空白。
本项目已因早期编码转换丢过一整片中文（v0.79.7 修的），所以这项必须有守卫。

# 判据

  1. 三语的**键集完全一致**（任何一边多或少都是缺陷）；
  2. 没有空字符串值（空值 = 界面上那块是空白，比键名外露更难发现）；
  3. 没有中文文案残留问号（编码损坏的形状）；
  4. 中文语境（zh_hans/zh_hant）的值不应是纯 ASCII 长串
     （那通常是漏译后直接留了英文）。

用法：python check_locale_consistency.py
退出码 0 = 通过。
"""
import json
import re
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parents[1]
LOCALES = Path(r"D:\奇怪的软件\octopus\web\src\locales")
FILES = ["zh_hans.json", "zh_hant.json", "en.json"]
CJK = re.compile(r"[\u4e00-\u9fff]")


def flatten(node, prefix=""):
    """把嵌套 dict 摊成 {路径: 值}，只收叶子（字符串）。"""
    out = {}
    if isinstance(node, dict):
        for k, v in node.items():
            out.update(flatten(v, f"{prefix}.{k}" if prefix else k))
    elif isinstance(node, str):
        out[prefix] = node
    return out


def main():
    trees = {}
    for name in FILES:
        path = LOCALES / name
        data = json.loads(path.read_text(encoding="utf-8"))
        trees[name] = flatten(data)

    base = trees["en.json"]
    problems = []

    # 1) 键集一致
    for name in FILES:
        keys = set(trees[name])
        missing = set(base) - keys
        extra = keys - set(base)
        if missing:
            problems.append(f"{name}: 缺 {len(missing)} 个键，例：{sorted(missing)[:5]}")
        if extra:
            problems.append(f"{name}: 多 {len(extra)} 个键，例：{sorted(extra)[:5]}")

    # 2) 空值 / 3) 问号乱码
    for name in FILES:
        for key, value in trees[name].items():
            if not value.strip():
                problems.append(f"{name}: {key} 是空值")
            if "????" in value:
                problems.append(f"{name}: {key} 含问号乱码")

    # 4) 中文语境不应是纯 ASCII 长串（漏译留英文）
    #    只查长度 >= 12 的：短串如 "form"/"API" 本就是合法英文借词。
    for name in ("zh_hans.json", "zh_hant.json"):
        for key, value in trees[name].items():
            if len(value) >= 12 and not CJK.search(value):
                en = base.get(key, "")
                # 三语本来就相同的不算（如单位、版本号）
                if en and value == en and not CJK.search(en):
                    continue
                problems.append(f"{name}: {key} 疑似漏译（纯 ASCII）：{value[:40]}")


    # 5) 命名空间存在性：代码里 useTranslations('X') 的 X 必须在三语里都存在。
    #    这一项的存在理由见 tools/check_locale_consistency.py 顶部注释：
    #    光校验"三语键集一致"抓不到"插入位置与组件查询路径不一致"——
    #    实测项目里就这样漏过一个整页显示原始键名的缺陷。
    src_root = REPO_ROOT / "web" / "src"
    ns_used = set()
    if src_root.exists():
        pat = re.compile(r"""(?:useTranslations|getTranslations)\(\s*['"]([^'"]+)['"]""")
        for f in src_root.rglob("*.ts*"):
            try:
                for m in pat.finditer(f.read_text(encoding="utf-8")):
                    ns_used.add(m.group(1))
            except OSError:
                continue
    for ns in sorted(ns_used):
        # 支持 home.performance 这样的嵌套命名空间：逐段下钻
        parts = ns.split(".")
        for name in FILES:
            node = json.loads((LOCALES / name).read_text(encoding="utf-8"))
            ok = True
            for p in parts:
                if isinstance(node, dict) and p in node:
                    node = node[p]
                else:
                    ok = False
                    break
            if not ok:
                problems.append(
                    f"{name}: 代码里用了 useTranslations('{ns}')，但该命名空间在文件里不存在"
                    f"（组件会显示原始键名）"
                )

    total = len(base)
    print(f"en.json 叶子键数: {total}")
    for name in FILES:
        print(f"  {name}: {len(trees[name])} 键")
    print()

    if problems:
        print(f"发现 {len(problems)} 个问题：")
        for p in problems[:40]:
            print("  -", p)
        if len(problems) > 40:
            print(f"  ……另有 {len(problems) - 40} 个")
        return 1

    print("三语一致：键集相同、无空值、无乱码、无漏译")
    return 0


if __name__ == "__main__":
    sys.exit(main())
