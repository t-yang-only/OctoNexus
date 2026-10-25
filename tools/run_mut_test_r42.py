import io
import subprocess

p = "internal/relay/handler.go"
s = io.open(p, encoding="utf-8").read()
old = """\t\t\t\tif err == nil {
\t\t\t\t\tc.Writer.Flush()
\t\t\t\t}"""

assert old in s
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, "", 1))

cmd = "go test ./internal/relay/ -run TestNonStreamResponseFlushes -count=1"
r = subprocess.run(cmd, shell=True, capture_output=True)
io.open(p, "w", encoding="utf-8", newline="").write(s)

stdout = r.stdout.decode("utf-8", errors="replace").strip()
stderr = r.stderr.decode("utf-8", errors="replace").strip()
print("RC:", r.returncode)
print("STDOUT:", stdout)
print("STDERR:", stderr)
