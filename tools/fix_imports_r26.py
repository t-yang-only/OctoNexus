import io

p = "internal/op/model_mapping_crud.go"
s = io.open(p, encoding="utf-8").read()

old = '''import (
\t"context"
\t"fmt"
\t"strings"

\t"github.com/t-yang-only/OctoNexus/internal/db"
\t"github.com/t-yang-only/OctoNexus/internal/model"
)'''

new = '''import (
\t"context"
\t"errors"
\t"fmt"
\t"strings"

\t"github.com/t-yang-only/OctoNexus/internal/db"
\t"github.com/t-yang-only/OctoNexus/internal/model"
\t"gorm.io/gorm"
)'''

assert old in s
s = s.replace(old, new, 1)
io.open(p, "w", encoding="utf-8", newline="").write(s)
print("IMPORTS_FIXED")
