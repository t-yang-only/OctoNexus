"""给三语 locale 补 WebDAV 云备份面板的文案。"""
import json
import os
import sys

ROOT = r"D:\奇怪的软件\octopus\web\src\locales"

TEXTS = {
    "zh_hans": {
        "title": "WebDAV 云备份",
        "subtitle": "定时把整库转储传到你的网盘；本机坏了还有一份",
        "url": "WebDAV 目录地址",
        "username": "用户名",
        "interval": "上传间隔（小时）",
        "keep": "远端保留份数（0 = 不清理）",
        "passwordHint": "口令只从服务端环境变量 OCTOPUS_WEBDAV_PASSWORD 读取，不写进设置表 —— "
                        "设置表会随导出转储与备份一起流转，把口令写进去等于把它复制到每一份备份里。",
        "runNow": "立即备份",
        "refresh": "刷新列表",
        "count": "远端共 {count} 份备份",
        "runSuccess": "已上传 {file}",
        "runFailed": "备份失败",
        "pruneWarning": "备份已成功，但清理旧文件失败：{reason}",
        "listFailed": "读取远端列表失败",
    },
    "zh_hant": {
        "title": "WebDAV 雲端備份",
        "subtitle": "定時把整庫傾印傳到你的網盤；本機壞了還有一份",
        "url": "WebDAV 目錄位址",
        "username": "使用者名稱",
        "interval": "上傳間隔（小時）",
        "keep": "遠端保留份數（0 = 不清理）",
        "passwordHint": "密碼只從伺服器環境變數 OCTOPUS_WEBDAV_PASSWORD 讀取，不寫進設定表 —— "
                        "設定表會隨匯出傾印與備份一起流轉，把密碼寫進去等於把它複製到每一份備份裡。",
        "runNow": "立即備份",
        "refresh": "重新整理清單",
        "count": "遠端共 {count} 份備份",
        "runSuccess": "已上傳 {file}",
        "runFailed": "備份失敗",
        "pruneWarning": "備份已成功，但清理舊檔案失敗：{reason}",
        "listFailed": "讀取遠端清單失敗",
    },
    "en": {
        "title": "WebDAV cloud backup",
        "subtitle": "Periodically push a full dump to your own cloud drive; a local failure no longer means losing everything",
        "url": "WebDAV directory URL",
        "username": "Username",
        "interval": "Upload interval (hours)",
        "keep": "Remote copies to keep (0 = never prune)",
        "passwordHint": "The password is read only from the server environment variable OCTOPUS_WEBDAV_PASSWORD and is "
                        "never stored in settings - settings travel inside every export and backup, so putting the "
                        "password there would copy it into each one of them.",
        "runNow": "Back up now",
        "refresh": "Refresh list",
        "count": "{count} backup(s) on the remote",
        "runSuccess": "Uploaded {file}",
        "runFailed": "Backup failed",
        "pruneWarning": "Backup succeeded, but pruning old files failed: {reason}",
        "listFailed": "Failed to read the remote list",
    },
}

failed = False
for lang, items in TEXTS.items():
    path = os.path.join(ROOT, "%s.json" % lang)
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    data["setting"]["webdavBackup"] = items
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        json.dump(data, fh, ensure_ascii=False, indent=4)
        fh.write("\n")
    print("%-8s keys=%d" % (lang, len(items)))

sys.exit(1 if failed else 0)
