import io

p = "internal/op/proxy_node.go"
s = io.open(p, encoding="utf-8").read()

old1 = '''func ProxyNodeUpdate(conn *gorm.DB, id int, in model.ProxyNodeInput) (model.ProxyNode, error) {
\tif conn == nil {
\t\tconn = proxyNodeDB(conn)
\t}
\tvar node model.ProxyNode
\tif err := conn.First(&node, id).Error; err != nil {
\t\treturn model.ProxyNode{}, err
\t}'''

new1 = '''func ProxyNodeUpdate(conn *gorm.DB, id int, in model.ProxyNodeInput) (model.ProxyNode, error) {
\tif conn == nil {
\t\tconn = proxyNodeDB(conn)
\t}
\tvar node model.ProxyNode
\tif err := conn.First(&node, id).Error; err != nil {
\t\tif errors.Is(err, gorm.ErrRecordNotFound) {
\t\t\treturn model.ProxyNode{}, NotFoundf("proxy node %d not found", id)
\t\t}
\t\treturn model.ProxyNode{}, err
\t}'''

assert old1 in s
s = s.replace(old1, new1, 1)

old2 = '''\treturn conn.Delete(&model.ProxyNode{}, id).Error
}'''

new2 = '''\tres := conn.Delete(&model.ProxyNode{}, id)
\tif res.Error != nil {
\t\treturn res.Error
\t}
\tif res.RowsAffected == 0 {
\t\treturn NotFoundf("proxy node %d not found", id)
\t}
\treturn nil
}'''

assert old2 in s
s = s.replace(old2, new2, 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)

p2 = "internal/server/handlers/proxy_node.go"
s2 = io.open(p2, encoding="utf-8").read()

old_up = '''\tnode, err := op.ProxyNodeUpdate(nil, id, in)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}'''
new_up = '''\tnode, err := op.ProxyNodeUpdate(nil, id, in)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}'''
assert old_up in s2
s2 = s2.replace(old_up, new_up, 1)

old_del = '''\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}
\tresp.Success(c, gin.H{"id": id, "removed": true})'''
new_del = '''\t\twriteOpError(c, err)
\t\treturn
\t}
\tresp.Success(c, gin.H{"id": id, "removed": true})'''
assert old_del in s2
s2 = s2.replace(old_del, new_del, 1)

io.open(p2, "w", encoding="utf-8", newline="").write(s2)

print("R29_APPLIED")
