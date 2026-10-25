import io

p = "internal/plugin/registry.go"
s = io.open(p, encoding="utf-8").read()

old_imp = 'import (\n\t"context"'
new_imp = 'import (\n\t"context"\n\t"errors"'
assert old_imp in s
s = s.replace(old_imp, new_imp, 1)

old_get = """func Get(ctx context.Context, slug string) (model.Plugin, error) {
\tconn := db.GetDB()
\tif conn == nil {
\t\treturn model.Plugin{}, fmt.Errorf("数据库未初始化")
\t}
\tvar row model.Plugin
\tif err := conn.WithContext(ctx).Where("slug = ?", slug).First(&row).Error; err != nil {
\t\treturn model.Plugin{}, fmt.Errorf("插件不存在：%s", slug)
\t}
\treturn row, nil
}"""

new_get = """func Get(ctx context.Context, slug string) (model.Plugin, error) {
\tconn := db.GetDB()
\tif conn == nil {
\t\treturn model.Plugin{}, fmt.Errorf("数据库未初始化")
\t}
\tvar row model.Plugin
\tif err := conn.WithContext(ctx).Where("slug = ?", slug).First(&row).Error; err != nil {
\t\tif errors.Is(err, gorm.ErrRecordNotFound) {
\t\t\treturn model.Plugin{}, op.NotFoundf("plugin %q not found", slug)
\t\t}
\t\treturn model.Plugin{}, fmt.Errorf("查询插件失败：%w", err)
\t}
\treturn row, nil
}"""
assert old_get in s
s = s.replace(old_get, new_get, 1)

old_rem = """func Remove(ctx context.Context, slug string) error {
\tconn := db.GetDB()
\tif conn == nil {
\t\treturn fmt.Errorf("数据库未初始化")
\t}
\tif running, _ := IsRunning(slug); running {
\t\treturn fmt.Errorf("插件 %s 正在运行，请先停止再删除", slug)
\t}
\treturn conn.WithContext(ctx).Where("slug = ?", slug).Delete(&model.Plugin{}).Error
}"""

new_rem = """func Remove(ctx context.Context, slug string) error {
\tconn := db.GetDB()
\tif conn == nil {
\t\treturn fmt.Errorf("数据库未初始化")
\t}
\tif running, _ := IsRunning(slug); running {
\t\treturn fmt.Errorf("插件 %s 正在运行，请先停止再删除", slug)
\t}
\tres := conn.WithContext(ctx).Where("slug = ?", slug).Delete(&model.Plugin{})
\tif res.Error != nil {
\t\treturn fmt.Errorf("删除插件记录失败：%w", res.Error)
\t}
\tif res.RowsAffected == 0 {
\t\treturn op.NotFoundf("plugin %q not found", slug)
\t}
\treturn nil
}"""
assert old_rem in s
s = s.replace(old_rem, new_rem, 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)

p2 = "internal/server/handlers/plugin.go"
s2 = io.open(p2, encoding="utf-8").read()

old_up = """\trow, err := plugin.Update(c.Request.Context(), c.Param("slug"), in)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""

new_up = """\trow, err := plugin.Update(c.Request.Context(), c.Param("slug"), in)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old_up in s2
s2 = s2.replace(old_up, new_up, 1)

old_rem2 = """func pluginRemove(c *gin.Context) {
\tif err := plugin.Remove(c.Request.Context(), c.Param("slug")); err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}
\tresp.Success(c, gin.H{})
}"""

new_rem2 = """func pluginRemove(c *gin.Context) {
\tif err := plugin.Remove(c.Request.Context(), c.Param("slug")); err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}
\tresp.Success(c, gin.H{})
}"""
assert old_rem2 in s2
s2 = s2.replace(old_rem2, new_rem2, 1)

old_st = """func pluginStart(c *gin.Context) {
\tstatus, err := plugin.Start(c.Request.Context(), c.Param("slug"))
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}
\tresp.Success(c, gin.H{"item": status})
}"""

new_st = """func pluginStart(c *gin.Context) {
\tstatus, err := plugin.Start(c.Request.Context(), c.Param("slug"))
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}
\tresp.Success(c, gin.H{"item": status})
}"""
assert old_st in s2
s2 = s2.replace(old_st, new_st, 1)

old_status = """func pluginStatus(c *gin.Context) {
\tstatus, err := plugin.Status(c.Request.Context(), c.Param("slug"))
\tif err != nil {
\t\tresp.Error(c, http.StatusNotFound, err.Error())
\t\treturn
\t}
\tresp.Success(c, gin.H{"item": status})
}"""

new_status = """func pluginStatus(c *gin.Context) {
\tstatus, err := plugin.Status(c.Request.Context(), c.Param("slug"))
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}
\tresp.Success(c, gin.H{"item": status})
}"""
assert old_status in s2
s2 = s2.replace(old_status, new_status, 1)

io.open(p2, "w", encoding="utf-8", newline="").write(s2)

print("R34_APPLIED")
