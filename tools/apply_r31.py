import io

p = "internal/op/sub2api.go"
s = io.open(p, encoding="utf-8").read()

old = '''func Sub2APIAccountProbe(ctx context.Context, id int) (string, error) {
\tconn := db.GetDB()
\tif conn == nil {
\t\treturn "", fmt.Errorf("数据库连接不可用")
\t}
\tvar account model.Sub2APIAccount
\tif err := conn.First(&account, id).Error; err != nil {
\t\treturn "", err
\t}'''

new = '''func Sub2APIAccountProbe(ctx context.Context, id int) (string, error) {
\tconn := db.GetDB()
\tif conn == nil {
\t\treturn "", fmt.Errorf("数据库连接不可用")
\t}
\tvar account model.Sub2APIAccount
\tif err := conn.First(&account, id).Error; err != nil {
\t\tif errors.Is(err, gorm.ErrRecordNotFound) {
\t\t\treturn "", ErrSub2APIAccountNotFound
\t\t}
\t\treturn "", err
\t}'''

assert old in s
s = s.replace(old, new, 1)
io.open(p, "w", encoding="utf-8", newline="").write(s)

p2 = "internal/server/handlers/sub2api.go"
s2 = io.open(p2, encoding="utf-8").read()

old2 = '''\tstatus, err := op.Sub2APIAccountProbe(c.Request.Context(), id)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadGateway, "探测失败: "+err.Error())
\t\treturn
\t}'''

new2 = '''\tstatus, err := op.Sub2APIAccountProbe(c.Request.Context(), id)
\tif err != nil {
\t\tif errors.Is(err, op.ErrSub2APIAccountNotFound) {
\t\t\tresp.Error(c, http.StatusNotFound, "账号不存在")
\t\t\treturn
\t\t}
\t\tresp.Error(c, http.StatusBadGateway, "探测失败: "+err.Error())
\t\treturn
\t}'''

assert old2 in s2
s2 = s2.replace(old2, new2, 1)
io.open(p2, "w", encoding="utf-8", newline="").write(s2)

print("R31_APPLIED")
