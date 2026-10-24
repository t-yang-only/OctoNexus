import io

p = "internal/op/llm.go"
s = io.open(p, encoding="utf-8").read()

old1 = '''func LLMUpdate(model model.LLMInfo, ctx context.Context) error {
\t_, ok := llmModelCache.Get(model.Name)
\tif !ok {
\t\treturn fmt.Errorf("model not found")
\t}'''

new1 = '''func LLMUpdate(model model.LLMInfo, ctx context.Context) error {
\t_, ok := llmModelCache.Get(model.Name)
\tif !ok {
\t\treturn NotFoundf("model %q not found", model.Name)
\t}'''

assert old1 in s
s = s.replace(old1, new1, 1)

old2 = '''func LLMDelete(modelName string, ctx context.Context) error {
\t_, ok := llmModelCache.Get(modelName)
\tif !ok {
\t\treturn fmt.Errorf("model not found")
\t}'''

new2 = '''func LLMDelete(modelName string, ctx context.Context) error {
\t_, ok := llmModelCache.Get(modelName)
\tif !ok {
\t\treturn NotFoundf("model %q not found", modelName)
\t}'''

assert old2 in s
s = s.replace(old2, new2, 1)

old3 = '''func LLMGet(name string) (model.LLMPrice, error) {
\tprice, ok := llmModelCache.Get(strings.ToLower(name))
\tif !ok {
\t\treturn model.LLMPrice{}, fmt.Errorf("model not found")
\t}'''

new3 = '''func LLMGet(name string) (model.LLMPrice, error) {
\tprice, ok := llmModelCache.Get(strings.ToLower(name))
\tif !ok {
\t\treturn model.LLMPrice{}, NotFoundf("model %q not found", name)
\t}'''

assert old3 in s
s = s.replace(old3, new3, 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)

p2 = "internal/server/handlers/model.go"
s2 = io.open(p2, encoding="utf-8").read()

old_up = '''\tif err := op.LLMUpdate(model, c.Request.Context()); err != nil {
\t\tresp.Error(c, http.StatusInternalServerError, err.Error())
\t\treturn
\t}'''
new_up = '''\tif err := op.LLMUpdate(model, c.Request.Context()); err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}'''
assert old_up in s2
s2 = s2.replace(old_up, new_up, 1)

old_del = '''\tif err := op.LLMDelete(req.Name, c.Request.Context()); err != nil {
\t\tresp.Error(c, http.StatusInternalServerError, err.Error())
\t\treturn
\t}'''
new_del = '''\tif err := op.LLMDelete(req.Name, c.Request.Context()); err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}'''
assert old_del in s2
s2 = s2.replace(old_del, new_del, 1)

io.open(p2, "w", encoding="utf-8", newline="").write(s2)

print("R28_APPLIED")
