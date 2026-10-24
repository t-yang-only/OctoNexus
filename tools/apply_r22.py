import io

p = "internal/op/group.go"
s = io.open(p, encoding="utf-8").read()

old1 = 'return fmt.Errorf("child group %d not found", item.ChildGroupID)'
new1 = 'return NotFoundf("child group %d not found", item.ChildGroupID)'
assert old1 in s
s = s.replace(old1, new1, 1)

old2 = 'return fmt.Errorf("group item not found")'
new2 = 'return NotFoundf("active item %d not found in group", *req.ActiveItemID)'
assert old2 in s
s = s.replace(old2, new2, 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)

p2 = "internal/server/handlers/group.go"
s2 = io.open(p2, encoding="utf-8").read()

old_cg = '''\tgroup, err := op.GroupCreate(&req, c.Request.Context())
\tif err != nil {
\t\tresp.Error(c, http.StatusInternalServerError, err.Error())
\t\treturn
\t}'''
new_cg = '''\tgroup, err := op.GroupCreate(&req, c.Request.Context())
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}'''
assert old_cg in s2
s2 = s2.replace(old_cg, new_cg, 1)
io.open(p2, "w", encoding="utf-8", newline="").write(s2)

print("R22_APPLIED")
