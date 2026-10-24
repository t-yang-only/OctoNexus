import io
import subprocess

CMD = 'go test ./internal/server/handlers/ -run "TestCreateGroupReturns404WhenChildGroupNotFound" -count=1'

def run():
    r = subprocess.run(CMD, shell=True, capture_output=True, text=True, encoding="utf-8", errors="replace")
    out = (r.stdout or "") + (r.stderr or "")
    fails = [l.strip() for l in out.splitlines() if "--- FAIL" in l]
    return fails

# Mutation R22-F1: change createGroup error handling to resp.Error(c, http.StatusInternalServerError, err.Error())
p = "internal/server/handlers/group.go"
s = io.open(p, encoding="utf-8").read()
old = "group, err := op.GroupCreate(&req, c.Request.Context())\n\tif err != nil {\n\t\twriteOpError(c, err)\n\t\treturn\n\t}"
mut = "group, err := op.GroupCreate(&req, c.Request.Context())\n\tif err != nil {\n\t\tresp.Error(c, http.StatusInternalServerError, err.Error())\n\t\treturn\n\t}"
assert old in s
io.open(p, "w", encoding="utf-8", newline="").write(s.replace(old, mut, 1))
f1 = run()
io.open(p, "w", encoding="utf-8", newline="").write(s)
print("MUTATION-R22-F1:", f1)

clean = run()
print("CLEAN RESTORE:", clean)
