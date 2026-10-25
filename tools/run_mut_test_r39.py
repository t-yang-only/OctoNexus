import io
import subprocess

p = "internal/server/handlers/pool.go"
s = io.open(p, encoding="utf-8").read()
old = """\tresult, err := pool.List(c.Request.Context(), filter, time.Now())
\tif err != nil {
\t\tif errors.Is(err, pool.ErrUnknownKind) {
\t\t\tresp.Error(c, http.StatusNotFound, err.Error())
\t\t\treturn
\t\t}
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""
mut = """\tresult, err := pool.List(c.Request.Context(), filter, time.Now())
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""
assert old in s
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, mut, 1))

cmd = "go test ./internal/server/handlers/ -run TestPoolEntriesReturns404WhenKindUnknown -count=1"
r = subprocess.run(cmd, shell=True, capture_output=True)
io.open(p, "w", encoding="utf-8", newline="").write(s)

stdout = r.stdout.decode("utf-8", errors="replace").strip()
stderr = r.stderr.decode("utf-8", errors="replace").strip()
print("RC:", r.returncode)
print("STDOUT:", stdout)
print("STDERR:", stderr)
