import io
import subprocess

CMD = 'go test ./internal/server/handlers/ -run "TestUpdateChannelReturns404WhenNotFound" -count=1'

def run():
    r = subprocess.run(CMD, shell=True, capture_output=True, text=True, encoding="utf-8", errors="replace")
    out = (r.stdout or "") + (r.stderr or "")
    fails = [l.strip() for l in out.splitlines() if "--- FAIL" in l]
    return fails

# Mutation R20-F1: revert to resp.Error(c, http.StatusInternalServerError, err.Error())
p = "internal/server/handlers/channel.go"
s = io.open(p, encoding="utf-8").read()
old = "writeOpError(c, err)"
mut = "resp.Error(c, http.StatusInternalServerError, err.Error())"
idx = s.find("func updateChannel(c *gin.Context) {")
assert idx != -1
pos = s.find(old, idx)
assert pos != -1
s_mut = s[:pos] + mut + s[pos+len(old):]

io.open(p, "w", encoding="utf-8", newline="").write(s_mut)
f1 = run()
io.open(p, "w", encoding="utf-8", newline="").write(s)
print("MUTATION-R20-F1:", f1)

clean = run()
print("CLEAN RESTORE:", clean)
