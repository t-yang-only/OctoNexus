# -*- coding: utf-8 -*-
"""给三语 locale 插入插件市场文案（需求1/2/3）。

两处插入：
  * extensions.menu.market  —— 子菜单项（market 排在最前，它是另外三类的统一入口）；
  * extensions.market.*     —— 市场面板自身。

规则同前：文本插入、保留 CRLF、每行带逗号、落盘前 json.loads 校验键集与兄弟键。
"""

import json
from pathlib import Path

LOCALES = Path(r"D:\奇怪的软件\octopus\web\src\locales")

MENU_ENTRIES = {
    "zh_hans.json": [("market", "插件市场")],
    "zh_hant.json": [("market", "插件市集")],
    "en.json": [("market", "Plugin market")],
}

MARKET_ENTRIES = {
    "zh_hans.json": [
        ("title", "插件市场"),
        ("subtitle", "装一个插件，让 octopus 多认识一家站点。上传插件文件或贴一个 GitHub 链接都可以。"),
        ("uploadTitle", "上传插件文件"),
        ("uploadHint", '选一个 .json 插件文件（上限 {size} 字节）。插件只描述「怎么接入」，不含账号密码。'),
        ("pickFile", "选择文件"),
        ("githubTitle", "从 GitHub 安装"),
        ("githubHint", "支持 /blob/<分支>/<文件>、raw.githubusercontent.com 直链、api.github.com/repos/<o>/<r>/contents/<路径> 三种形态。"),
        ("githubPlaceholder", "https://github.com/<用户>/<仓库>/blob/<分支>/<文件>.json"),
        ("install", "安装"),
        ("installFailed", "安装失败"),
        ("installed", "已解析插件：{name}"),
        ("installedTitle", "{name} 已就绪"),
        ("kind", "类型"),
        ("kindBalance", "账号余额"),
        ("kindPool", "号池"),
        ("nextStep", "插件已通过校验并转交载荷，但还没写入。去下面确认后即可投入使用："),
        ("goCollectors", "去采集凭据确认"),
        ("goAdapters", "去号池适配器确认"),
        ("kindsTitle", "支持装什么"),
        ("requires", "必须声明"),
        ("noCredential", "插件文件不得携带账号密码 —— 分享插件会连带分享凭据。请在安装时填写自己的。"),
    ],
    "zh_hant.json": [
        ("title", "插件市集"),
        ("subtitle", "裝一個外掛，讓 octopus 多認識一家站台。上傳外掛檔或貼一個 GitHub 連結都可以。"),
        ("uploadTitle", "上傳外掛檔"),
        ("uploadHint", "選一個 .json 外掛檔（上限 {size} 位元組）。外掛只描述「怎麼接入」，不含帳號密碼。"),
        ("pickFile", "選擇檔案"),
        ("githubTitle", "從 GitHub 安裝"),
        ("githubHint", "支援 /blob/<分支>/<檔案>、raw.githubusercontent.com 直連、api.github.com/repos/<o>/<r>/contents/<路徑> 三種形態。"),
        ("githubPlaceholder", "https://github.com/<用戶>/<倉庫>/blob/<分支>/<檔案>.json"),
        ("install", "安裝"),
        ("installFailed", "安裝失敗"),
        ("installed", "已解析外掛：{name}"),
        ("installedTitle", "{name} 已就緒"),
        ("kind", "類型"),
        ("kindBalance", "帳號餘額"),
        ("kindPool", "號池"),
        ("nextStep", "外掛已通過校驗並轉交載荷，但還沒寫入。到下面確認後即可投入使用："),
        ("goCollectors", "去採集憑證確認"),
        ("goAdapters", "去號池介面卡確認"),
        ("kindsTitle", "支援裝什麼"),
        ("requires", "必須聲明"),
        ("noCredential", "外掛檔不得攜帶帳號密碼 —— 分享外掛會連帶分享憑證。請在安裝時填寫自己的。"),
    ],
    "en.json": [
        ("title", "Plugin market"),
        ("subtitle", "Install a plugin to teach octopus about one more site. Upload a file or paste a GitHub link."),
        ("uploadTitle", "Upload a plugin file"),
        ("uploadHint", "Pick a .json plugin file (max {size} bytes). A plugin only describes how to connect; it carries no credentials."),
        ("pickFile", "Choose file"),
        ("githubTitle", "Install from GitHub"),
        ("githubHint", "Accepts /blob/<branch>/<file>, raw.githubusercontent.com direct links, and api.github.com/repos/<owner>/<repo>/contents/<path>."),
        ("githubPlaceholder", "https://github.com/<owner>/<repo>/blob/<branch>/<file>.json"),
        ("install", "Install"),
        ("installFailed", "Install failed"),
        ("installed", "Plugin parsed: {name}"),
        ("installedTitle", "{name} is ready"),
        ("kind", "Kind"),
        ("kindBalance", "Account balance"),
        ("kindPool", "Pool"),
        ("nextStep", "The plugin passed validation and its payload was handed over, but nothing is written yet. Confirm below to put it to work:"),
        ("goCollectors", "Go to collectors"),
        ("goAdapters", "Go to pool adapters"),
        ("kindsTitle", "What you can install"),
        ("requires", "Requires"),
        ("noCredential", "Plugin files must not carry credentials - sharing a plugin would share the account. Fill yours in during setup."),
    ],
}


def insert_menu(name, entries):
    """往 extensions.menu 里加一项。

    锚点取 menu 段的**第一个子键**，插到它前面：menu 的第一个子键是 plugins
    （既有顺序），它的前一行必然是该段的收尾 `},`。这样不依赖任何具体键名。
    """
    path = LOCALES / name
    raw = path.read_text(encoding="utf-8", newline="")
    eol = "\r\n" if "\r\n" in raw[:2000] else "\n"
    lines = raw.splitlines(keepends=True)

    def bare(index):
        return lines[index].rstrip("\r\n")

    if any(bare(i) == '      "market":' for i in range(len(lines))):
        print(f"{name}: menu.market already present, skip")
        return
    # 定位 extensions.menu 段，再找它的第一个子键。
    menu_start = next(i for i in range(len(lines)) if bare(i) == '    "menu": {')
    first_child = None
    for i in range(menu_start + 1, len(lines)):
        stripped = bare(i)
        if stripped == '    },':
            raise SystemExit(f"{name}: menu 段内找不到子键")
        if stripped.startswith('      "') and stripped.endswith(','):
            first_child = i
            break
    if first_child is None:
        raise SystemExit(f"{name}: 定位 menu 第一个子键失败")

    indent = lines[first_child][: len(lines[first_child]) - len(lines[first_child].lstrip())]
    added = [f'{indent}"{key}": {json.dumps(value, ensure_ascii=False)},{eol}' for key, value in entries]
    lines[first_child:first_child] = added
    body = "".join(lines)

    parsed = json.loads(body)
    menu = parsed["extensions"]["menu"]
    want = {k for k, _ in entries}
    if not want.issubset(set(menu)):
        raise SystemExit(f"{name}: extensions.menu 缺键 {want - set(menu)}")
    for sibling in ("plugins", "collectors", "adapters"):
        if sibling not in menu:
            raise SystemExit(f"{name}: menu.{sibling} 消失")

    path.write_text(body, encoding="utf-8", newline="")
    print(f"{name}: extensions.menu +{len(added)} lines")


for locale, entries in MENU_ENTRIES.items():
    insert_menu(locale, entries)

# market 块是 extensions 下的新对象，插在 menu 之后：锚点取 menu 段的收尾行不好定位，
# 改为插在 adapters 之前（extensions 的既有子键），用 "adapters": { 作锚点。
for locale, entries in MARKET_ENTRIES.items():
    path = LOCALES / locale
    raw = path.read_text(encoding="utf-8", newline="")
    eol = "\r\n" if "\r\n" in raw[:2000] else "\n"
    lines = raw.splitlines(keepends=True)

    def bare(index):
        return lines[index].rstrip("\r\n")

    if any(bare(i) == '    "market": {' for i in range(len(lines))):
        print(f"{locale}: already has extensions.market, skip")
        continue
    anchor = next(i for i in range(len(lines)) if bare(i) == '    "adapters": {')
    block = [f'    "market": {{{eol}']
    for i, (key, value) in enumerate(entries):
        comma = "," if i < len(entries) - 1 else ""
        block.append(f'      "{key}": {json.dumps(value, ensure_ascii=False)}{comma}{eol}')
    block.append(f'    }},{eol}')
    lines[anchor:anchor] = block
    body = "".join(lines)

    parsed = json.loads(body)
    got = parsed["extensions"]["market"]
    want = {k for k, _ in entries}
    if set(got) != want:
        raise SystemExit(f"{locale}: market 键集不匹配 {set(got) ^ want}")
    for sibling in ("menu", "plugins", "collectors", "adapters"):
        if sibling not in parsed["extensions"]:
            raise SystemExit(f"{locale}: extensions.{sibling} 消失")

    path.write_text(body, encoding="utf-8", newline="")
    print(f"{locale}: extensions.market +{len(block)} lines, keys ok")

print("done")
