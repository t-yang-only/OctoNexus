import io
import subprocess

CMD = 'go test ./internal/op/ -run "TestSettingListRedactsPoolDeclarativeAdapters" -count=1'

def run():
    r = subprocess.run(CMD, shell=True, capture_output=True, text=True, encoding="utf-8", errors="replace")
    out = (r.stdout or "") + (r.stderr or "")
    fails = [l.strip() for l in out.splitlines() if "--- FAIL" in l]
    return fails

# Mutation F1: remove redaction
p = "internal/op/setting.go"
s = io.open(p, encoding="utf-8").read()
a = "if key == model.SettingKeyPoolDeclarativeAdapters {"
m = "if false && key == model.SettingKeyPoolDeclarativeAdapters {"
assert a in s
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(a, m, 1))
f1 = run()
io.open(p, "w", encoding="utf-8", newline="").write(s)
print("MUTATION-R16-F1:", f1)

clean = run()
print("CLEAN RESTORE:", clean)
