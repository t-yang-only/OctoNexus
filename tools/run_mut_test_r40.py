import io
import subprocess

p = "internal/server/handlers/usage_report.go"
s = io.open(p, encoding="utf-8").read()
old = """\tif trimmed != "" {
\t\tif !model.IsValidUsageReportPeriod(trimmed) {
\t\t\treturn "", fmt.Errorf("invalid period %q (want daily, weekly, or monthly)", trimmed)
\t\t}
\t\treturn model.UsageReportPeriod(trimmed), nil
\t}"""
mut = """\tif trimmed != "" {
\t\tif false && !model.IsValidUsageReportPeriod(trimmed) {
\t\t\treturn "", fmt.Errorf("invalid period %q (want daily, weekly, or monthly)", trimmed)
\t\t}
\t\treturn model.UsageReportDaily, nil
\t}"""
assert old in s
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, mut, 1))

cmd = "go test ./internal/server/handlers/ -run TestPreviewUsageReportRejectsInvalidPeriod -count=1"
r = subprocess.run(cmd, shell=True, capture_output=True)
io.open(p, "w", encoding="utf-8", newline="").write(s)

stdout = r.stdout.decode("utf-8", errors="replace").strip()
stderr = r.stderr.decode("utf-8", errors="replace").strip()
print("RC:", r.returncode)
print("STDOUT:", stdout)
print("STDERR:", stderr)
