package relay

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// T-trace-006：测试请求标记的判定、落库与"绝不改变行为"三条契约。

// TestIsTestHeaderValueAcceptsOnlyExplicitTrue 钉住判定规则只认明确写法。
//
// 这个标记决定一条日志算不算画像样本，多认一种写法就多一分"以为没标、其实标上了"
// 或反过来的风险，而这类错账不会有人去核对。因此这里逐值断言，包括几个**看着像真、
// 但其实不认**的写法（yes/on/2）—— 它们不是"顺手支持一下"的候选，而是明确的口径边界。
func TestIsTestHeaderValueAcceptsOnlyExplicitTrue(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"  1  ", true},
		{"false", false},
		{"FALSE", false},
		{"0", false},
		{"", false},
		{"   ", false},
		{"yes", false},
		{"on", false},
		{"2", false},
		{"true-ish", false},
	}
	for _, item := range cases {
		if got := isTestHeaderValue(item.raw); got != item.want {
			t.Fatalf("isTestHeaderValue(%q)=%v，应为 %v", item.raw, got, item.want)
		}
	}
}

// TestIsTestRequestReadsHeader 覆盖"从请求头读"这一段（纯函数判据之外的接线）。
func TestIsTestRequestReadsHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, raw := range []string{"true", "1", "false", "yes", ""} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		if raw != "" {
			req.Header.Set(RelayTestHeader, raw)
		}
		c.Request = req
		if got, want := isTestRequest(c), isTestHeaderValue(raw); got != want {
			t.Fatalf("isTestRequest(header=%q)=%v，应为 %v", raw, got, want)
		}
	}
}

// TestTestFlagIsPersisted 覆盖落库：标记随请求写进 relay_logs。
//
// 落点在 finishLocked（三条终态路径的汇合处）而不是 markSucceeded —— 测试请求
// 也可能失败（探活打到坏渠道时正是如此），失败的那条同样必须被标出来，
// 否则"哪几条是我自己发的"又答不上来了。
func TestTestFlagIsPersisted(t *testing.T) {
	openInsightTestDB(t)

	marked := newRequestState(nil, "probe-group", 1, 2, "{}", "", 0, true)
	marked.markSucceeded("{}", nil)
	if row := loadInsightRow(t, marked.ID); !row.IsTest {
		t.Fatalf("测试标记没有落库（request_id=%d）", marked.ID)
	}

	plain := newRequestState(nil, "probe-group", 1, 2, "{}", "", 0, false)
	plain.markSucceeded("{}", nil)
	if row := loadInsightRow(t, plain.ID); row.IsTest {
		t.Fatalf("未声明的请求被误标成测试请求（request_id=%d）—— 那会把真实流量从画像里剔掉", plain.ID)
	}
}

// TestTestFlagIsPersistedOnFailedRequest 反向覆盖失败路径：探活失败的那条也要带标记。
func TestTestFlagIsPersistedOnFailedRequest(t *testing.T) {
	openInsightTestDB(t)

	failedTest := newRequestState(nil, "probe-group", 1, 2, "{}", "", 0, true)
	failedTest.markFailed(errTestCanceled{}, "", nil, stopReasonBudget, stopSourceConfig)
	if row := loadInsightRow(t, failedTest.ID); !row.IsTest {
		t.Fatalf("失败路径丢了测试标记（request_id=%d）", failedTest.ID)
	}
}

// TestTestFlagChangesNothingButTheFlag 钉住「标记不改变行为」的字段层面。
//
// 语义层面的守卫在下面那条 AST 判据里；这里先钉最直接的一条：同一份请求参数，
// 带标记与不带标记构造出来的状态，除标记本身外**所有与选路相关的字段必须逐字相同**。
// 若哪天有人在构造函数里顺手用 isTest 调了别的东西（比如跳过冷却），这里会红。
func TestTestFlagChangesNothingButTheFlag(t *testing.T) {
	plain := newRequestState(nil, "grp", 3, 2, `{"a":1}`, "high", 7, false)
	marked := newRequestState(nil, "grp", 3, 2, `{"a":1}`, "high", 7, true)

	if plain.Model != marked.Model || plain.GroupID != marked.GroupID ||
		plain.Protocol != marked.Protocol || plain.ReasoningEffort != marked.ReasoningEffort ||
		plain.apiKeyID != marked.apiKeyID || plain.body != marked.body {
		t.Fatalf("标记改变了与选路相关的字段：plain=%+v marked=%+v", plain, marked)
	}
	if plain.IsTest || !marked.IsTest {
		t.Fatalf("标记本身没写对：plain.IsTest=%v marked.IsTest=%v", plain.IsTest, marked.IsTest)
	}
}

// TestTestFlagIsNeverReadByRouting 是这一族的**结构守卫**：测试标记绝不能被选路逻辑读到。
//
// 为什么必须机械化：这个标记存在的理由是"它产生的请求与真实流量走完全相同的路径"。
// 一旦某个选路/冷却/重试分支读了它（哪怕只是"测试请求就不计入冷却"这种看着合理的改动），
// 带标记的请求就不再代表真实流量 —— 而这个标记会**摧毁它自己的用途**：
// 我们用它来验证真实路径通不通，它却让被验证的路径变了。
//
// 行为用例抓不到这类问题：读一下 IsTest 往往不改变单次请求的结果（冷却要多次才显形），
// 而"验证通过了"与"路径已经不同"在测试输出里长得一样。
//
// 允许的读取位置：把标记写出去的地方。展示层（实时看板）也是合法的。
func TestTestFlagIsNeverReadByRouting(t *testing.T) {
	// 函数名 → 为什么它可以读 IsTest。
	allowed := map[string]string{
		"finishLocked":         "落库：把标记写进 relay_logs",
		"recordSystemOneLog":   "落库：自定义协议同一条口径",
		"publishRequestLocked": "展示：把状态推给实时看板",
	}

	fset := token.NewFileSet()
	scanned := 0
	walkErr := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Errorf("解析 %s 失败: %v", filepath.ToSlash(path), parseErr)
			return nil
		}
		scanned++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				sel, ok := node.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "IsTest" {
					return true
				}
				if _, ok := allowed[fn.Name.Name]; !ok {
					t.Errorf("%s: 函数 %s 读了 IsTest —— 测试标记只能被落库与展示读取，"+
						"一旦选路/冷却/重试逻辑读了它，带标记的请求就不再代表真实流量（这个标记会摧毁自己的用途）",
						filepath.ToSlash(fset.Position(sel.Pos()).String()), fn.Name.Name)
				}
				return true
			})
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("遍历 relay 包失败: %v", walkErr)
	}
	if scanned < 5 {
		t.Fatalf("只扫到 %d 个 .go 文件，守卫自身失效了", scanned)
	}
}
