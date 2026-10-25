import io

p_store = "internal/poolstore/store.go"
s_store = io.open(p_store, encoding="utf-8").read()

old_rem = """func Remove(kind string) error {
\tmu.Lock()
\tdefer mu.Unlock()

\tkind = strings.TrimSpace(kind)
\tif kind == "" {
\t\treturn fmt.Errorf("%w: 空 kind", pool.ErrUnknownKind)
\t}
\tstored, err := specs()
\tif err != nil {
\t\treturn err
\t}
\tif err := pool.UnregisterRuntime(kind); err != nil {
\t\treturn err
\t}
\treturn save(drop(stored, kind))
}"""

new_rem = """// Get 按 kind 读取单个声明式适配器（已脱敏）。
func Get(kind string) (pool.DeclarativeSpec, error) {
\tstored, err := specs()
\tif err != nil {
\t\treturn pool.DeclarativeSpec{}, err
\t}
\tfor _, spec := range stored {
\t\tif spec.Kind == kind {
\t\t\treturn Redact(spec), nil
\t\t}
\t}
\treturn pool.DeclarativeSpec{}, fmt.Errorf("%w: kind %q not found", pool.ErrUnknownKind, kind)
}

func Remove(kind string) error {
\tmu.Lock()
\tdefer mu.Unlock()

\tkind = strings.TrimSpace(kind)
\tif kind == "" {
\t\treturn fmt.Errorf("%w: 空 kind", pool.ErrUnknownKind)
\t}
\tif pool.BuiltinKind(kind) {
\t\treturn fmt.Errorf("%w: kind %q 是内置适配器，不能移除", pool.ErrUnknownKind, kind)
\t}
\tstored, err := specs()
\tif err != nil {
\t\treturn err
\t}
\tfoundInStore := false
\tfor _, s := range stored {
\t\tif s.Kind == kind {
\t\t\tfoundInStore = true
\t\t\tbreak
\t\t}
\t}
\truntimeErr := pool.UnregisterRuntime(kind)
\tif !foundInStore && runtimeErr != nil {
\t\treturn fmt.Errorf("%w: kind %q not found", pool.ErrUnknownKind, kind)
\t}
\treturn save(drop(stored, kind))
}"""
assert old_rem in s_store
s_store = s_store.replace(old_rem, new_rem, 1)
io.open(p_store, "w", encoding="utf-8", newline="").write(s_store)

p_hpool = "internal/server/handlers/pool.go"
s_hpool = io.open(p_hpool, encoding="utf-8").read()

old_imp_pool = 'import (\n\t"encoding/csv"'
new_imp_pool = 'import (\n\t"encoding/csv"\n\t"errors"'
assert old_imp_pool in s_hpool
s_hpool = s_hpool.replace(old_imp_pool, new_imp_pool, 1)

old_entries = """\tresult, err := pool.List(c.Request.Context(), filter, time.Now())
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""

new_entries = """\tresult, err := pool.List(c.Request.Context(), filter, time.Now())
\tif err != nil {
\t\tif errors.Is(err, pool.ErrUnknownKind) {
\t\t\tresp.Error(c, http.StatusNotFound, err.Error())
\t\t\treturn
\t\t}
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""
assert old_entries in s_hpool
s_hpool = s_hpool.replace(old_entries, new_entries, 1)

old_export = """\trows, failures, err := pool.ExportRows(c.Request.Context(), filter, time.Now())
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""

new_export = """\trows, failures, err := pool.ExportRows(c.Request.Context(), filter, time.Now())
\tif err != nil {
\t\tif errors.Is(err, pool.ErrUnknownKind) {
\t\t\tresp.Error(c, http.StatusNotFound, err.Error())
\t\t\treturn
\t\t}
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""
assert old_export in s_hpool
s_hpool = s_hpool.replace(old_export, new_export, 1)

old_batch = """\tresult, err := pool.Batch(c.Request.Context(), request, time.Now())
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""

new_batch = """\tresult, err := pool.Batch(c.Request.Context(), request, time.Now())
\tif err != nil {
\t\tif errors.Is(err, pool.ErrUnknownKind) {
\t\t\tresp.Error(c, http.StatusNotFound, err.Error())
\t\t\treturn
\t\t}
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""
assert old_batch in s_hpool
s_hpool = s_hpool.replace(old_batch, new_batch, 1)
io.open(p_hpool, "w", encoding="utf-8", newline="").write(s_hpool)

p_adapters = "internal/server/handlers/pool_adapters.go"
s_adapters = io.open(p_adapters, encoding="utf-8").read()

old_route = """\t\tAddRoute(
\t\t\trouter.NewRoute("/adapters/:kind", http.MethodDelete).
\t\t\t\tHandle(poolAdapterRemove),
\t\t)"""

new_route = """\t\tAddRoute(
\t\t\trouter.NewRoute("/adapters/:kind", http.MethodGet).
\t\t\t\tHandle(poolAdapterGet),
\t\t).
\t\tAddRoute(
\t\t\trouter.NewRoute("/adapters/:kind", http.MethodDelete).
\t\t\t\tHandle(poolAdapterRemove),
\t\t)"""
assert old_route in s_adapters
s_adapters = s_adapters.replace(old_route, new_route, 1)

new_fn = """// poolAdapterGet 获取单个声明式适配器详情（已脱敏）。
func poolAdapterGet(c *gin.Context) {
\tkind := c.Param("kind")
\tspec, err := poolstore.Get(kind)
\tif err != nil {
\t\tstatus := http.StatusInternalServerError
\t\tif errors.Is(err, pool.ErrUnknownKind) {
\t\t\tstatus = http.StatusNotFound
\t\t}
\t\tresp.Error(c, status, err.Error())
\t\treturn
\t}
\tresp.Success(c, gin.H{"item": spec})
}

// poolAdapterRemove 移除一个声明式适配器（内置适配器会被拒绝）。"""

old_rem_fn = "// poolAdapterRemove 移除一个声明式适配器（内置适配器会被拒绝）。"
assert old_rem_fn in s_adapters
s_adapters = s_adapters.replace(old_rem_fn, new_fn, 1)

io.open(p_adapters, "w", encoding="utf-8", newline="").write(s_adapters)

print("R39_APPLIED")
