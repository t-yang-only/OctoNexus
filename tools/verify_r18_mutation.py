import io
import subprocess

CMD = 'go test ./internal/op/ -run "TestAPIKeyCreatePersistsAllowedCIDRs" -count=1'

def run():
    r = subprocess.run(CMD, shell=True, capture_output=True, text=True, encoding="utf-8", errors="replace")
    out = (r.stdout or "") + (r.stderr or "")
    fails = [l.strip() for l in out.splitlines() if "--- FAIL" in l]
    return fails

Q = chr(34)
p = "internal/op/apikey.go"
s = io.open(p, encoding="utf-8").read()
target = Q + "supported_models" + Q + ", " + Q + "allowed_cidrs" + Q
mut = Q + "supported_models" + Q
assert target in s

# Mutate: remove allowed_cidrs from Select
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(target, mut, 1))
f1 = run()
io.open(p, "w", encoding="utf-8", newline="").write(s)
print("MUTATION-R18-F1:", f1)

clean = run()
print("CLEAN RESTORE:", clean)
