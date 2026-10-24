import io

p = "internal/op/channel.go"
s = io.open(p, encoding="utf-8").read()

old1 = '''func ChannelDetailGet(id int) (model.ChannelDetail, error) {
\tchannel, ok := channelCache.Get(id)
\tif !ok {
\t\treturn model.ChannelDetail{}, fmt.Errorf("channel not found")
\t}'''
new1 = '''func ChannelDetailGet(id int) (model.ChannelDetail, error) {
\tchannel, ok := channelCache.Get(id)
\tif !ok {
\t\treturn model.ChannelDetail{}, NotFoundf("channel %d not found", id)
\t}'''
assert old1 in s
s = s.replace(old1, new1, 1)

old2 = '''func ChannelEnabled(id int, enabled bool, ctx context.Context) error {
\tchannel, ok := channelCache.Get(id)
\tif !ok {
\t\treturn fmt.Errorf("channel not found")
\t}'''
new2 = '''func ChannelEnabled(id int, enabled bool, ctx context.Context) error {
\tchannel, ok := channelCache.Get(id)
\tif !ok {
\t\treturn NotFoundf("channel %d not found", id)
\t}'''
assert old2 in s
s = s.replace(old2, new2, 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)

p2 = "internal/server/handlers/channel.go"
s2 = io.open(p2, encoding="utf-8").read()

old_d = '''\tdetail, err := op.ChannelDetailGet(id)
\tif err != nil {
\t\tresp.Error(c, http.StatusNotFound, err.Error())
\t\treturn
\t}'''
new_d = '''\tdetail, err := op.ChannelDetailGet(id)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}'''
assert old_d in s2
s2 = s2.replace(old_d, new_d, 1)

old_e = '''\tif err := op.ChannelEnabled(request.ID, request.Enabled, c.Request.Context()); err != nil {
\t\tresp.Error(c, http.StatusInternalServerError, err.Error())
\t\treturn
\t}'''
new_e = '''\tif err := op.ChannelEnabled(request.ID, request.Enabled, c.Request.Context()); err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}'''
assert old_e in s2
s2 = s2.replace(old_e, new_e, 1)

io.open(p2, "w", encoding="utf-8", newline="").write(s2)

print("R25_APPLIED")
