import io

p = "internal/op/group.go"
s = io.open(p, encoding="utf-8").read()

old1 = '''func GroupGet(id int) (model.Group, error) {
\tgroup, ok := groupCache.Get(id)
\tif !ok {
\t\treturn model.Group{}, fmt.Errorf("group not found")
\t}'''
new1 = '''func GroupGet(id int) (model.Group, error) {
\tgroup, ok := groupCache.Get(id)
\tif !ok {
\t\treturn model.Group{}, NotFoundf("group %d not found", id)
\t}'''
assert old1 in s
s = s.replace(old1, new1, 1)

old2 = '''func GroupUpdate(id int, req *model.GroupUpdateRequest, ctx context.Context) (*model.Group, error) {
\toldGroup, ok := groupCache.Get(id)
\tif !ok {
\t\treturn nil, fmt.Errorf("group not found")
\t}'''
new2 = '''func GroupUpdate(id int, req *model.GroupUpdateRequest, ctx context.Context) (*model.Group, error) {
\toldGroup, ok := groupCache.Get(id)
\tif !ok {
\t\treturn nil, NotFoundf("group %d not found", id)
\t}'''
assert old2 in s
s = s.replace(old2, new2, 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)

p2 = "internal/server/handlers/group.go"
s2 = io.open(p2, encoding="utf-8").read()

old_h = '''\toldGroup, err := op.GroupGet(id)
\tif err != nil {
\t\tresp.Error(c, http.StatusNotFound, err.Error())
\t\treturn
\t}
\tgroup, err := op.GroupUpdate(id, &req, c.Request.Context())
\tif err != nil {
\t\tresp.Error(c, http.StatusInternalServerError, err.Error())
\t\treturn
\t}'''

new_h = '''\toldGroup, err := op.GroupGet(id)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}
\tgroup, err := op.GroupUpdate(id, &req, c.Request.Context())
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}'''
assert old_h in s2
s2 = s2.replace(old_h, new_h, 1)
io.open(p2, "w", encoding="utf-8", newline="").write(s2)

print("R21_APPLIED")
