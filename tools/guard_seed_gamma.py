import io

P = r'internal/op/sub2api_r5_test.go'
s = io.open(P, encoding='utf-8').read()
old = '''\t\tseed, err := ChannelDetailGet(created.ChannelID)
\t\tif err != nil {
\t\t\tt.Fatalf("第 %d 轮读渠道失败: %v", round, err)
\t\t}
\t\tseed.Models = append(seed.Models, "manual-gamma")
\t\tseed.Grants = append(seed.Grants, model.ChannelGrantConfig{
\t\t\tModelName: "manual-gamma",
\t\t\tKeyName:   "default",
\t\t\tProtocols: model.ProtocolOpenAIChatCompletion,
\t\t})
\t\tif _, err := ChannelUpdate(&seed, ctx); err != nil {
\t\t\tt.Fatalf("第 %d 轮写入人工模型失败: %v", round, err)
\t\t}'''
new = '''\t\tseed, err := ChannelDetailGet(created.ChannelID)
\t\tif err != nil {
\t\t\tt.Fatalf("第 %d 轮读渠道失败: %v", round, err)
\t\t}
\t\thasGamma := false
\t\tfor _, m := range seed.Models {
\t\t\tif m == "manual-gamma" {
\t\t\t\thasGamma = true
\t\t\t\tbreak
\t\t\t}
\t\t}
\t\tif !hasGamma {
\t\t\tseed.Models = append(seed.Models, "manual-gamma")
\t\t\tseed.Grants = append(seed.Grants, model.ChannelGrantConfig{
\t\t\t\tModelName: "manual-gamma",
\t\t\t\tKeyName:   "default",
\t\t\t\tProtocols: model.ProtocolOpenAIChatCompletion,
\t\t\t})
\t\t\tif _, err := ChannelUpdate(&seed, ctx); err != nil {
\t\t\t\tt.Fatalf("第 %d 轮写入人工模型失败: %v", round, err)
\t\t\t}
\t\t}'''
assert old in s, 'seed anchor not found'
s = s.replace(old, new, 1)
io.open(P, 'w', encoding='utf-8', newline='').write(s)
print('SEED_GUARDED')
