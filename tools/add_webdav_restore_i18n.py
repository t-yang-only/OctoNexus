"""补云端恢复相关的三语文案。"""
import json
import os
import sys

ROOT = r"D:\奇怪的软件\octopus\web\src\locales"

TEXTS = {
    "zh_hans": {
        "restore": "恢复",
        "restoreClickAgain": "再点一次确认",
        "restoreConfirm": "确认从 {file} 恢复？这会改动当前数据。",
        "restoreSuccess": "已恢复。恢复前快照：{path}",
        "restoreFailed": "恢复失败",
        "restoreHint": "恢复是增量合并（新行插入、已有配置更新），不会删除备份之后新增的行；"
                       "误恢复时用响应里给出的「恢复前快照」走上面的导入回退。",
    },
    "zh_hant": {
        "restore": "還原",
        "restoreClickAgain": "再點一次確認",
        "restoreConfirm": "確認從 {file} 還原？這會改動目前資料。",
        "restoreSuccess": "已還原。還原前快照：{path}",
        "restoreFailed": "還原失敗",
        "restoreHint": "還原是增量合併（新行插入、既有設定更新），不會刪除備份之後新增的行；"
                       "誤還原時用回應裡給出的「還原前快照」走上面的匯入回退。",
    },
    "en": {
        "restore": "Restore",
        "restoreClickAgain": "Click again to confirm",
        "restoreConfirm": "Restore from {file}? This will modify your current data.",
        "restoreSuccess": "Restored. Pre-restore snapshot: {path}",
        "restoreFailed": "Restore failed",
        "restoreHint": "Restore is an incremental merge (insert new rows, update existing config). "
                       "It does not delete rows added after the backup. If you restore by mistake, "
                       "use the pre-restore snapshot path from the response with the import above.",
    },
}

for lang, items in TEXTS.items():
    path = os.path.join(ROOT, "%s.json" % lang)
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    section = data["setting"]["webdavBackup"]
    for k, v in items.items():
        section[k] = v
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        json.dump(data, fh, ensure_ascii=False, indent=4)
        fh.write("\n")
    print("%-8s total keys=%d" % (lang, len(section)))

sys.exit(0)
