import io

p = "internal/op/channel.go"
s = io.open(p, encoding="utf-8").read()

old1 = """func ChannelGet(id int) (model.Channel, error) {
\tchannel, ok := channelCache.Get(id)
\tif !ok {
\t\treturn model.Channel{}, fmt.Errorf("channel not found")
\t}
\treturn channel, nil
}"""

new1 = """func ChannelGet(id int) (model.Channel, error) {
\tchannel, ok := channelCache.Get(id)
\tif !ok {
\t\treturn model.Channel{}, NotFoundf("channel %d not found", id)
\t}
\treturn channel, nil
}"""
assert old1 in s
s = s.replace(old1, new1, 1)

old2 = """func ChannelModelGet(id int) (model.ChannelModel, error) {
\tchannelModel, ok := channelModelCache.Get(id)
\tif !ok {
\t\treturn model.ChannelModel{}, fmt.Errorf("channel model not found")
\t}
\treturn channelModel, nil
}"""

new2 = """func ChannelModelGet(id int) (model.ChannelModel, error) {
\tchannelModel, ok := channelModelCache.Get(id)
\tif !ok {
\t\treturn model.ChannelModel{}, NotFoundf("channel model %d not found", id)
\t}
\treturn channelModel, nil
}"""
assert old2 in s
s = s.replace(old2, new2, 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)
print("CHANNEL_GET_UPDATED")
