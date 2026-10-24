import io

p = "internal/op/sub2api.go"
s = io.open(p, encoding="utf-8").read()
old = '\t"context"'
new = '\t"context"\n\t"errors"'
assert old in s
s = s.replace(old, new, 1)
io.open(p, "w", encoding="utf-8", newline="").write(s)
print("IMPORT_ADDED")
