import io
import subprocess

p = "internal/server/handlers/sub2api.go"
s = io.open(p, encoding="utf-8").read()
old = """\tif err != nil {
\t\tif errors.Is(err, op.ErrSub2APIAccountNotFound) {
\t\t\tresp.Error(c, http.StatusNotFound, "账号不存在")
\t\t\treturn
\t\t}
\t\tresp.Error(c, http.StatusBadGateway, "探测失败: "+err.Error())
\t\treturn
\t}"""
mut = """\tif err != nil {
\t\tresp.Error(c, http.StatusBadGateway, "探测失败: "+err.Error())
\t\treturn
\t}"""
assert old in s
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, mut, 1))

cmd = "go test ./internal/server/handlers/ -run TestSub2APIAccountProbeReturns404WhenNotFound -count=1"
r = subprocess.run(cmd, shell=True, capture_output=True)
io.open(p, "w", encoding="utf-8", newline="").write(s)

stdout = r.stdout.decode("utf-8", errors="replace").strip()
stderr = r.stderr.decode("utf-8", errors="replace").strip()
print("RC:", r.returncode)
print("STDOUT:", stdout)
print("STDERR:", stderr)
