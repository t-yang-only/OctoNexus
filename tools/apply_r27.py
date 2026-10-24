import io

p = "internal/op/model_mapping_crud.go"
s = io.open(p, encoding="utf-8").read()

old = '''func ModelMappingDelete(ctx context.Context, id int) error {
\tif err := db.GetDB().WithContext(ctx).Delete(&model.ModelMapping{}, id).Error; err != nil {
\t\treturn fmt.Errorf("delete model mapping %d: %w", id, err)
\t}
\treturn ModelMappingRefresh(ctx)
}'''

new = '''func ModelMappingDelete(ctx context.Context, id int) error {
\tres := db.GetDB().WithContext(ctx).Delete(&model.ModelMapping{}, id)
\tif res.Error != nil {
\t\treturn fmt.Errorf("delete model mapping %d: %w", id, res.Error)
\t}
\tif res.RowsAffected == 0 {
\t\treturn NotFoundf("model mapping %d not found", id)
\t}
\treturn ModelMappingRefresh(ctx)
}'''

assert old in s
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, new, 1))

p2 = "internal/server/handlers/model_mapping.go"
s2 = io.open(p2, encoding="utf-8").read()

old2 = '''\tif err := op.ModelMappingDelete(c.Request.Context(), id); err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}'''

new2 = '''\tif err := op.ModelMappingDelete(c.Request.Context(), id); err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}'''

assert old2 in s2
io.open(p2, "w", encoding="utf-8", newline="").write(s2.replace(old2, new2, 1))

print("R27_APPLIED")
