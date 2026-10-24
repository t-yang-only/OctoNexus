import io
import subprocess

p = "internal/server/handlers/proxy_node.go"
s = io.open(p, encoding="utf-8").read()
old = "if err != nil {\n\t\twriteOpError(c, err)\n\t\treturn\n\t}\n\tresp.Success(c, gin.H{\"item\": node})"
mut = "if err != nil {\n\t\tresp.Error(c, http.StatusBadRequest, err.Error())\n\t\treturn\n\t}\n\tresp.Success(c, gin.H{\"item\": node})"
assert old in s
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, mut, 1))

cmd = "go test ./internal/server/handlers/ -run TestProxyNodeUpdateReturns404WhenNotFound -count=1"
r = subprocess.run(cmd, shell=True, capture_output=True)
io.open(p, "w", encoding="utf-8", newline="").write(s)

stdout = r.stdout.decode("utf-8", errors="replace").strip()
stderr = r.stderr.decode("utf-8", errors="replace").strip()
print("RC:", r.returncode)
print("STDOUT:", stdout)
print("STDERR:", stderr)
