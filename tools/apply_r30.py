import io

p = "internal/op/proxy_node.go"
s = io.open(p, encoding="utf-8").read()

old1 = '''func ProxySubscriptionUpdate(conn *gorm.DB, id int, in model.ProxySubscriptionInput) (model.ProxySubscription, error) {
\tif conn == nil {
\t\tconn = proxyNodeDB(conn)
\t}
\tvar sub model.ProxySubscription
\tif err := conn.First(&sub, id).Error; err != nil {
\t\treturn model.ProxySubscription{}, err
\t}'''

new1 = '''func ProxySubscriptionUpdate(conn *gorm.DB, id int, in model.ProxySubscriptionInput) (model.ProxySubscription, error) {
\tif conn == nil {
\t\tconn = proxyNodeDB(conn)
\t}
\tvar sub model.ProxySubscription
\tif err := conn.First(&sub, id).Error; err != nil {
\t\tif errors.Is(err, gorm.ErrRecordNotFound) {
\t\t\treturn model.ProxySubscription{}, NotFoundf("proxy subscription %d not found", id)
\t\t}
\t\treturn model.ProxySubscription{}, err
\t}'''

assert old1 in s
s = s.replace(old1, new1, 1)

old2 = '''\treturn conn.Delete(&model.ProxySubscription{}, id).Error
}

// ProxySubscriptionRefresh 拉取订阅并导入其节点。'''

new2 = '''\tres := conn.Delete(&model.ProxySubscription{}, id)
\tif res.Error != nil {
\t\treturn fmt.Errorf("delete proxy subscription: %w", res.Error)
\t}
\tif res.RowsAffected == 0 {
\t\treturn NotFoundf("proxy subscription %d not found", id)
\t}
\treturn nil
}

// ProxySubscriptionRefresh 拉取订阅并导入其节点。'''

assert old2 in s
s = s.replace(old2, new2, 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)

p2 = "internal/server/handlers/proxy_node.go"
s2 = io.open(p2, encoding="utf-8").read()

old_up = '''\tsub, err := op.ProxySubscriptionUpdate(nil, id, in)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}'''

new_up = '''\tsub, err := op.ProxySubscriptionUpdate(nil, id, in)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}'''

assert old_up in s2
s2 = s2.replace(old_up, new_up, 1)

old_del = '''\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}
\tresp.Success(c, gin.H{"id": id, "removed": true})
}

func proxySubscriptionRefresh(c *gin.Context) {'''

new_del = '''\t\twriteOpError(c, err)
\t\treturn
\t}
\tresp.Success(c, gin.H{"id": id, "removed": true})
}

func proxySubscriptionRefresh(c *gin.Context) {'''

assert old_del in s2
s2 = s2.replace(old_del, new_del, 1)

io.open(p2, "w", encoding="utf-8", newline="").write(s2)

print("R30_APPLIED")
