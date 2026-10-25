import io

p = "internal/relay/handler.go"
s = io.open(p, encoding="utf-8").read()

target = """\t\t\t\tn, err := c.Writer.Write(result.body)
\t\t\t\tif err == nil && n != len(result.body) {
\t\t\t\t\terr = io.ErrShortWrite
\t\t\t\t}
\t\t\t\tif err != nil {"""

replacement = """\t\t\t\tn, err := c.Writer.Write(result.body)
\t\t\t\tif err == nil && n != len(result.body) {
\t\t\t\t\terr = io.ErrShortWrite
\t\t\t\t}
\t\t\t\tif err == nil {
\t\t\t\t\tc.Writer.Flush()
\t\t\t\t}
\t\t\t\tif err != nil {"""

assert target in s
s = s.replace(target, replacement, 1)
io.open(p, "w", encoding="utf-8", newline="").write(s)
print("NONSTREAM_FLUSH_APPLIED")
