import subprocess
import sys
import io

p = "internal/relay/handler.go"
orig = io.open(p, encoding="utf-8").read()

# 变异: 删除 publishDecision 里的 requestIDHeaderName 注入
mutated = orig.replace('\tc.Writer.Header().Set(requestIDHeaderName, strconv.FormatUint(request.ID, 10))\n\trequest.setDecision', '\trequest.setDecision', 1)
assert mutated != orig, "变异未命中"

io.open(p, "w", encoding="utf-8", newline="\n").write(mutated)
try:
    res = subprocess.run(["go", "test", "-count=1", "-v", "./internal/relay", "-run", "TestRequestIDHeaderInvariant"], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    print("MUTATION RETURN CODE:", res.returncode)
    if res.returncode != 0:
        print("MUTATION KILLED SUCCESSFULLY (PASS)")
    else:
        print("MUTATION SURVIVED (FAIL)")
        sys.exit(1)
finally:
    io.open(p, "w", encoding="utf-8", newline="\n").write(orig)
    print("ORIGINAL RESTORED")
