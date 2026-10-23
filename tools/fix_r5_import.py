import io

p = r'internal/op/sub2api_r5_test.go'
s = io.open(p, encoding='utf-8').read()
old = '\t"strings"\n\t"sync"'
assert old in s, 'import anchor not found'
s = s.replace(old, '\t"strings"\n\t"sync"\n\t"time"', 1)
io.open(p, 'w', encoding='utf-8', newline='').write(s)
print('IMPORT_ADDED')
