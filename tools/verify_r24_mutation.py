import io
import subprocess

CMD = 'go test ./internal/op/ -run "TestDBImportIncrementalFiltersInvalidSettings" -count=1'

def run():
    r = subprocess.run(CMD, shell=True, capture_output=True, text=True, encoding="utf-8", errors="replace")
    out = (r.stdout or "") + (r.stderr or "")
    fails = [l.strip() for l in out.splitlines() if "--- FAIL" in l]
    return fails

# Mutation R24-F1: 移除 createUpsertSettings 中的校验，直接保存原 rows
p = "internal/op/backup.go"
s = io.open(p, encoding="utf-8").read()
old = "}).Create(&validRows)"
mut = "}).Create(&rows)"
assert old in s
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, mut, 1))
f1 = run()
io.open(p, "w", encoding="utf-8", newline="").write(s)
print("MUTATION-R24-F1:", f1)

clean = run()
print("CLEAN RESTORE:", clean)
