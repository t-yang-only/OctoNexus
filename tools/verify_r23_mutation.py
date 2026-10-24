import io
import subprocess

CMD = 'go test ./internal/server/handlers/ -run "TestSetSettingModelInfoUpdateIntervalSucceeds" -count=1'

def run():
    r = subprocess.run(CMD, shell=True, capture_output=True, text=True, encoding="utf-8", errors="replace")
    out = (r.stdout or "") + (r.stderr or "")
    fails = [l.strip() for l in out.splitlines() if "--- FAIL" in l]
    return fails

# Mutation R23-F1: 模拟在 switch 中因 strconv.Atoi 错误直接返回 400 截断的旧行为，
# 我们通过故意注入一个错误截断来验证测试敏感性：
p = "internal/server/handlers/setting.go"
s = io.open(p, encoding="utf-8").read()
old = 'if hours, err := strconv.Atoi(setting.Value); err == nil {\n\t\t\ttask.Update(string(setting.Key), time.Duration(hours)*time.Hour)\n\t\t}'
mut = 'resp.Error(c, http.StatusBadRequest, "forced mutation error")\n\t\treturn'
assert old in s
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, mut, 1))
f1 = run()
io.open(p, "w", encoding="utf-8", newline="").write(s)
print("MUTATION-R23-F1:", f1)

clean = run()
print("CLEAN RESTORE:", clean)
