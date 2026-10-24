import io
import subprocess

p = "internal/server/handlers/model_mapping.go"
s = io.open(p, encoding="utf-8").read()
old = "writeOpError(c, err)"
mut = "resp.Error(c, http.StatusBadRequest, err.Error())"
idx = s.find("func toggleModelMapping")
pos = s.find(old, idx)
s_mut = s[:pos] + mut + s[pos+len(old):]
io.open(p, "w", encoding="utf-8", newline="").write(s_mut)

cmd = "go test ./internal/server/handlers/ -run TestToggleModelMappingReturns404WhenNotFound -count=1"
r = subprocess.run(cmd, shell=True, capture_output=True)
io.open(p, "w", encoding="utf-8", newline="").write(s)

stdout = r.stdout.decode("utf-8", errors="replace").strip()
stderr = r.stderr.decode("utf-8", errors="replace").strip()
print("RC:", r.returncode)
print("STDOUT:", stdout)
print("STDERR:", stderr)
