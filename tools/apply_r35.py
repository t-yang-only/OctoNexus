import io

p = "internal/op/balance_manual.go"
s = io.open(p, encoding="utf-8").read()

old_set = """\tif err := db.GetDB().Model(&model.Channel{}).Where("id = ?", channelID).Updates(updates).Error; err != nil {
\t\treturn err
\t}
\treturn ReloadChannel(channelID)"""

new_set = """\tres := db.GetDB().Model(&model.Channel{}).Where("id = ?", channelID).Updates(updates)
\tif res.Error != nil {
\t\treturn res.Error
\t}
\tif res.RowsAffected == 0 {
\t\treturn NotFoundf("channel %d not found", channelID)
\t}
\treturn ReloadChannel(channelID)"""
assert old_set in s
s = s.replace(old_set, new_set, 1)

old_rel = """func ReloadChannel(channelID int) error {
\tvar channel model.Channel
\tif err := db.GetDB().First(&channel, channelID).Error; err != nil {
\t\treturn err
\t}
\tchannelCache.Set(channel.ID, channel)
\treturn nil
}"""

new_rel = """func ReloadChannel(channelID int) error {
\tvar channel model.Channel
\tif err := db.GetDB().First(&channel, channelID).Error; err != nil {
\t\tif errors.Is(err, gorm.ErrRecordNotFound) {
\t\t\treturn NotFoundf("channel %d not found", channelID)
\t\t}
\t\treturn err
\t}
\tchannelCache.Set(channel.ID, channel)
\treturn nil
}"""
assert old_rel in s
s = s.replace(old_rel, new_rel, 1)

old_imp = 'import (\n\t"strings"'
new_imp = 'import (\n\t"errors"\n\t"strings"'
assert old_imp in s
s = s.replace(old_imp, new_imp, 1)

old_imp2 = '"github.com/t-yang-only/OctoNexus/internal/model"\n)'
new_imp2 = '"github.com/t-yang-only/OctoNexus/internal/model"\n\t"gorm.io/gorm"\n)'
assert old_imp2 in s
s = s.replace(old_imp2, new_imp2, 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)

p2 = "internal/server/handlers/balance_admin.go"
s2 = io.open(p2, encoding="utf-8").read()

old_h = """\tif err := op.ChannelManualBalanceSet(id, body.Points, body.Note); err != nil {
\t\tresp.Error(c, http.StatusInternalServerError, err.Error())
\t\treturn
\t}"""

new_h = """\tif err := op.ChannelManualBalanceSet(id, body.Points, body.Note); err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old_h in s2
s2 = s2.replace(old_h, new_h, 1)

io.open(p2, "w", encoding="utf-8", newline="").write(s2)

print("R35_APPLIED")
