import io

p = "internal/op/backup.go"
s = io.open(p, encoding="utf-8").read()

old = '''func createUpsertSettings(tx *gorm.DB, rows []model.Setting) (int64, error) {
\tif len(rows) == 0 {
\t\treturn 0, nil
\t}
\tresult := tx.Clauses(clause.OnConflict{
\t\tColumns:   []clause.Column{{Name: "key"}},
\t\tDoUpdates: clause.AssignmentColumns([]string{"value"}),
\t}).Create(&rows)
\treturn result.RowsAffected, result.Error
}'''

new = '''func createUpsertSettings(tx *gorm.DB, rows []model.Setting) (int64, error) {
\tif len(rows) == 0 {
\t\treturn 0, nil
\t}
\t// R24-F1: 导入设置时必须执行合法性校验，防止非法或越界的设置项写入系统产生异常
\tvalidRows := make([]model.Setting, 0, len(rows))
\tfor _, row := range rows {
\t\tif err := row.Validate(); err != nil {
\t\t\tcontinue
\t\t}
\t\tvalidRows = append(validRows, row)
\t}
\tif len(validRows) == 0 {
\t\treturn 0, nil
\t}
\tresult := tx.Clauses(clause.OnConflict{
\t\tColumns:   []clause.Column{{Name: "key"}},
\t\tDoUpdates: clause.AssignmentColumns([]string{"value"}),
\t}).Create(&validRows)
\treturn result.RowsAffected, result.Error
}'''

assert old in s
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, new, 1))
print("R24_APPLIED")
