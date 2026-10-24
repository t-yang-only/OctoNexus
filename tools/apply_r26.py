import io

p = "internal/op/model_mapping_crud.go"
s = io.open(p, encoding="utf-8").read()

old1 = '''func ModelMappingUpdate(ctx context.Context, id int, req *model.ModelMappingUpdateRequest) (*model.ModelMapping, error) {
\tvar item model.ModelMapping
\tif err := db.GetDB().WithContext(ctx).First(&item, id).Error; err != nil {
\t\treturn nil, fmt.Errorf("find model mapping %d: %w", id, err)
\t}'''
new1 = '''func ModelMappingUpdate(ctx context.Context, id int, req *model.ModelMappingUpdateRequest) (*model.ModelMapping, error) {
\tvar item model.ModelMapping
\tif err := db.GetDB().WithContext(ctx).First(&item, id).Error; err != nil {
\t\tif errors.Is(err, gorm.ErrRecordNotFound) {
\t\t\treturn nil, NotFoundf("model mapping %d not found", id)
\t\t}
\t\treturn nil, fmt.Errorf("find model mapping %d: %w", id, err)
\t}'''
assert old1 in s
s = s.replace(old1, new1, 1)

old2 = '''func ModelMappingGet(ctx context.Context, id int) (*model.ModelMapping, error) {
\tvar item model.ModelMapping
\tif err := db.GetDB().WithContext(ctx).First(&item, id).Error; err != nil {
\t\treturn nil, fmt.Errorf("get model mapping %d: %w", id, err)
\t}
\treturn &item, nil
}'''
new2 = '''func ModelMappingGet(ctx context.Context, id int) (*model.ModelMapping, error) {
\tvar item model.ModelMapping
\tif err := db.GetDB().WithContext(ctx).First(&item, id).Error; err != nil {
\t\tif errors.Is(err, gorm.ErrRecordNotFound) {
\t\t\treturn nil, NotFoundf("model mapping %d not found", id)
\t\t}
\t\treturn nil, fmt.Errorf("get model mapping %d: %w", id, err)
\t}
\treturn &item, nil
}'''
assert old2 in s
s = s.replace(old2, new2, 1)

old3 = '''func ModelMappingToggle(ctx context.Context, id int, enabled bool) (*model.ModelMapping, error) {
\tif err := db.GetDB().WithContext(ctx).Model(&model.ModelMapping{}).
\t\tWhere("id = ?", id).Update("enabled", enabled).Error; err != nil {
\t\treturn nil, fmt.Errorf("toggle model mapping %d: %w", id, err)
\t}
\tif err := ModelMappingRefresh(ctx); err != nil {
\t\treturn nil, err
\t}
\treturn ModelMappingGet(ctx, id)
}'''
new3 = '''func ModelMappingToggle(ctx context.Context, id int, enabled bool) (*model.ModelMapping, error) {
\tres := db.GetDB().WithContext(ctx).Model(&model.ModelMapping{}).
\t\tWhere("id = ?", id).Update("enabled", enabled)
\tif res.Error != nil {
\t\treturn nil, fmt.Errorf("toggle model mapping %d: %w", id, res.Error)
\t}
\tif res.RowsAffected == 0 {
\t\treturn nil, NotFoundf("model mapping %d not found", id)
\t}
\tif err := ModelMappingRefresh(ctx); err != nil {
\t\treturn nil, err
\t}
\treturn ModelMappingGet(ctx, id)
}'''
assert old3 in s
s = s.replace(old3, new3, 1)

if 'errors' not in s:
    s = s.replace('import (\n', 'import (\n\t"errors"\n', 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)

p2 = "internal/server/handlers/model_mapping.go"
s2 = io.open(p2, encoding="utf-8").read()

old_up = '''\titem, err := op.ModelMappingUpdate(c.Request.Context(), id, &req)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}'''
new_up = '''\titem, err := op.ModelMappingUpdate(c.Request.Context(), id, &req)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}'''
assert old_up in s2
s2 = s2.replace(old_up, new_up, 1)

old_tog = '''\titem, err := op.ModelMappingToggle(c.Request.Context(), id, body.Enabled)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}'''
new_tog = '''\titem, err := op.ModelMappingToggle(c.Request.Context(), id, body.Enabled)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}'''
assert old_tog in s2
s2 = s2.replace(old_tog, new_tog, 1)

io.open(p2, "w", encoding="utf-8", newline="").write(s2)

print("R26_APPLIED")
