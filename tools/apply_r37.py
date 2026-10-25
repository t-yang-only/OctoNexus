import io

p1 = "internal/server/handlers/group_quick.go"
s1 = io.open(p1, encoding="utf-8").read()

old1 = """\tresult, err := op.GroupQuickCreate(c.Request.Context(), &req)
\tif err != nil {
\t\t// 这些错误都是"用户该改输入"型（渠道不存在/模型无可用凭据/同名分组成员不同），
\t\t// 一律 400 并带上原文，不回 500 —— 500 会让人以为是服务端故障。
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""

new1 = """\tresult, err := op.GroupQuickCreate(c.Request.Context(), &req)
\tif err != nil {
\t\tif op.IsNotFound(err) {
\t\t\tresp.Error(c, http.StatusNotFound, err.Error())
\t\t\treturn
\t\t}
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""
assert old1 in s1
s1 = s1.replace(old1, new1, 1)
io.open(p1, "w", encoding="utf-8", newline="").write(s1)

p2 = "internal/server/handlers/group.go"
s2 = io.open(p2, encoding="utf-8").read()

old2 = """\tgroup, err := op.GroupGet(id)
\tif err != nil {
\t\tresp.Error(c, http.StatusNotFound, err.Error())
\t\treturn
\t}"""

new2 = """\tgroup, err := op.GroupGet(id)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old2 in s2
s2 = s2.replace(old2, new2, 1)
io.open(p2, "w", encoding="utf-8", newline="").write(s2)

p3 = "internal/server/handlers/channel_verify.go"
s3 = io.open(p3, encoding="utf-8").read()

old3 = """\tdetail, err := op.ChannelDetailGet(id)
\tif err != nil {
\t\tresp.Error(c, http.StatusNotFound, err.Error())
\t\treturn
\t}"""

new3 = """\tdetail, err := op.ChannelDetailGet(id)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old3 in s3
s3 = s3.replace(old3, new3, 1)
io.open(p3, "w", encoding="utf-8", newline="").write(s3)

p4 = "internal/server/handlers/channel_upstream.go"
s4 = io.open(p4, encoding="utf-8").read()

old4 = """\tdetail, err := op.ChannelDetailGet(id)
\tif err != nil {
\t\tresp.Error(c, http.StatusNotFound, err.Error())
\t\treturn
\t}"""

new4 = """\tdetail, err := op.ChannelDetailGet(id)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
assert old4 in s4
s4 = s4.replace(old4, new4, 1)
io.open(p4, "w", encoding="utf-8", newline="").write(s4)

print("R37_APPLIED")
