import io
import subprocess

p = "internal/server/handlers/setting.go"
s = io.open(p, encoding="utf-8").read()
old = """\tif err := op.SettingSetString(setting.Key, setting.Value); err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}"""
mut = """\tif err := op.SettingSetString(setting.Key, setting.Value); err != nil {
\t\tresp.Error(c, http.StatusInternalServerError, err.Error())
\t\treturn
\t}"""
assert old in s
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, mut, 1))

cmd = "go test ./internal/server/handlers/ -run TestSetSettingReturns404WhenKeyNotFound -count=1"
r = subprocess.run(cmd, shell=True, capture_output=True)
io.open(p, "w", encoding="utf-8", newline="").write(s)

stdout = r.stdout.decode("utf-8", errors="replace").strip()
stderr = r.stderr.decode("utf-8", errors="replace").strip()
print("RC:", r.returncode)
print("STDOUT:", stdout)
print("STDERR:", stderr)
