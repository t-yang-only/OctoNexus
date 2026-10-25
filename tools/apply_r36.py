import io

p = "internal/op/credential_source.go"
s = io.open(p, encoding="utf-8").read()

old_imp = 'import (\n\t"context"'
new_imp = 'import (\n\t"context"\n\t"errors"'
assert old_imp in s
s = s.replace(old_imp, new_imp, 1)

old_imp2 = '"github.com/t-yang-only/OctoNexus/internal/secret"\n)'
new_imp2 = '"github.com/t-yang-only/OctoNexus/internal/secret"\n\t"gorm.io/gorm"\n)'
assert old_imp2 in s
s = s.replace(old_imp2, new_imp2, 1)

old_row = """func credentialSourceRow(ctx context.Context, id int) (model.CredentialSource, error) {
\tvar row model.CredentialSource
\tif id <= 0 {
\t\treturn row, fmt.Errorf("采集凭据 id 非法")
\t}
\tif err := db.GetDB().WithContext(ctx).First(&row, id).Error; err != nil {
\t\treturn row, fmt.Errorf("采集凭据 #%d 不存在：%v", id, err)
\t}
\treturn row, nil
}"""

new_row = """func credentialSourceRow(ctx context.Context, id int) (model.CredentialSource, error) {
\tvar row model.CredentialSource
\tif id <= 0 {
\t\treturn row, fmt.Errorf("采集凭据 id 非法")
\t}
\tif err := db.GetDB().WithContext(ctx).First(&row, id).Error; err != nil {
\t\tif errors.Is(err, gorm.ErrRecordNotFound) {
\t\t\treturn row, NotFoundf("credential source %d not found", id)
\t\t}
\t\treturn row, fmt.Errorf("采集凭据 #%d 不存在：%v", id, err)
\t}
\treturn row, nil
}"""
assert old_row in s
s = s.replace(old_row, new_row, 1)

old_del = """func CredentialSourceDelete(ctx context.Context, id int) error {
\tif err := db.GetDB().WithContext(ctx).Where("id = ?", id).Delete(&model.CredentialSource{}).Error; err != nil {
\t\treturn err
\t}
\tcollectorState.Lock()
\tdelete(collectorState.sessions, id)
\tdelete(collectorState.steps, id)
\tdelete(collectorState.tokens, id)
\tcollectorState.Unlock()
\treturn nil
}"""

new_del = """func CredentialSourceDelete(ctx context.Context, id int) error {
\tres := db.GetDB().WithContext(ctx).Where("id = ?", id).Delete(&model.CredentialSource{})
\tif res.Error != nil {
\t\treturn res.Error
\t}
\tif res.RowsAffected == 0 {
\t\treturn NotFoundf("credential source %d not found", id)
\t}
\tcollectorState.Lock()
\tdelete(collectorState.sessions, id)
\tdelete(collectorState.steps, id)
\tdelete(collectorState.tokens, id)
\tcollectorState.Unlock()
\treturn nil
}"""
assert old_del in s
s = s.replace(old_del, new_del, 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)

p2 = "internal/server/handlers/collector.go"
s2 = io.open(p2, encoding="utf-8").read()

old_get = """func collectorSourceGet(c *gin.Context) {
\tid, ok := collectorID(c)
\tif !ok {
\t\treturn
\t}
\titem, err := op.CredentialSourceGet(c.Request.Context(), id)
\tif err != nil {
\t\tresp.Error(c, http.StatusNotFound, err.Error())
\t\treturn
\t}
\tresp.Success(c, gin.H{"item": item})
}"""

new_get = """func collectorSourceGet(c *gin.Context) {
\tid, ok := collectorID(c)
\tif !ok {
\t\treturn
\t}
\titem, err := op.CredentialSourceGet(c.Request.Context(), id)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}
\tresp.Success(c, gin.H{"item": item})
}"""
assert old_get in s2
s2 = s2.replace(old_get, new_get, 1)

old_up = """\titem, err := op.CredentialSourceSave(c.Request.Context(), id, in)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""

new_up = """\titem, err := op.CredentialSourceSave(c.Request.Context(), id, in)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old_up in s2
s2 = s2.replace(old_up, new_up, 1)

old_del2 = """func collectorSourceDelete(c *gin.Context) {
\tid, ok := collectorID(c)
\tif !ok {
\t\treturn
\t}
\tif err := op.CredentialSourceDelete(c.Request.Context(), id); err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}
\tresp.Success(c, gin.H{})
}"""

new_del2 = """func collectorSourceDelete(c *gin.Context) {
\tid, ok := collectorID(c)
\tif !ok {
\t\treturn
\t}
\tif err := op.CredentialSourceDelete(c.Request.Context(), id); err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}
\tresp.Success(c, gin.H{})
}"""
assert old_del2 in s2
s2 = s2.replace(old_del2, new_del2, 1)

old_run = """\treading, result, err := op.CredentialSourceRunNow(c.Request.Context(), id)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""

new_run = """\treading, result, err := op.CredentialSourceRunNow(c.Request.Context(), id)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old_run in s2
s2 = s2.replace(old_run, new_run, 1)

old_exp = """\titem, pack, err := op.CredentialSourceExportPack(c.Request.Context(), id)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""

new_exp = """\titem, pack, err := op.CredentialSourceExportPack(c.Request.Context(), id)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old_exp in s2
s2 = s2.replace(old_exp, new_exp, 1)

old_log_st = """\titem, err := op.CredentialSourceStartLogin(c.Request.Context(), id)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""

new_log_st = """\titem, err := op.CredentialSourceStartLogin(c.Request.Context(), id)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old_log_st in s2
s2 = s2.replace(old_log_st, new_log_st, 1)

old_log_fin = """\titem, err := op.CredentialSourceFinishLogin(c.Request.Context(), id)
\tif err != nil {
\t\t// 会话可能已经存下、只是试读失败：把最新状态一并带回，面板才能显示真实处境。
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""

new_log_fin = """\titem, err := op.CredentialSourceFinishLogin(c.Request.Context(), id)
\tif err != nil {
\t\t// 会话可能已经存下、只是试读失败：把最新状态一并带回，面板才能显示真实处境。
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old_log_fin in s2
s2 = s2.replace(old_log_fin, new_log_fin, 1)

old_sess_clr = """\titem, err := op.CredentialSourceClearSession(c.Request.Context(), id)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""

new_sess_clr = """\titem, err := op.CredentialSourceClearSession(c.Request.Context(), id)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old_sess_clr in s2
s2 = s2.replace(old_sess_clr, new_sess_clr, 1)

io.open(p2, "w", encoding="utf-8", newline="").write(s2)

print("R36_APPLIED")
