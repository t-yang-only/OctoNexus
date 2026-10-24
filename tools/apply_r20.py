import io

p = "internal/op/channel.go"
s = io.open(p, encoding="utf-8").read()
old = 'return nil, fmt.Errorf("channel not found")'
new = 'return nil, NotFoundf("channel %d not found", detail.ID)'
assert old in s
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, new, 1))

p2 = "internal/server/handlers/channel.go"
s2 = io.open(p2, encoding="utf-8").read()
old2 = '''\tchannel, err := op.ChannelUpdate(&req, c.Request.Context())
\tif err != nil {
\t\tresp.Error(c, http.StatusInternalServerError, err.Error())
\t\treturn
\t}'''
new2 = '''\tchannel, err := op.ChannelUpdate(&req, c.Request.Context())
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}'''
assert old2 in s2
io.open(p2, "w", encoding="utf-8", newline="").write(s2.replace(old2, new2, 1))

print("APPLIED_R20")
