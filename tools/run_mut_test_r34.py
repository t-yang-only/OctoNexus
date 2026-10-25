import io
import subprocess

p = "internal/server/handlers/plugin.go"
s = io.open(p, encoding="utf-8").read()
old = """\trow, err := plugin.Update(c.Request.Context(), c.Param("slug"), in)
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
mut = """\trow, err := plugin.Update(c.Request.Context(), c.Param("slug"), in)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}"""
assert old in s
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, mut, 1))

cmd = "go test ./internal/server/handlers/ -run TestPluginUpdateReturns404WhenNotFound -count=1"
r = subprocess.run(cmd, shell=True, capture_output=True)
io.open(p, "w", encoding="utf-8", newline="").write(s)

stdout = r.stdout.decode("utf-8", errors="replace").strip()
stderr = r.stderr.decode("utf-8", errors="replace").strip()
print("RC:", r.returncode)
print("STDOUT:", stdout)
print("STDERR:", stderr)
