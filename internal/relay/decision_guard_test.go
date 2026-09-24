package relay

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestRelayLogDecisionIsNeverHandWritten 守 T-decision-001 的「唯一源头」契约：
// 落库的判定文本必须由 Decision.Text() 产出，不能手工拼字符串。
//
// 为什么这里要用 AST 而不是行为用例：手工拼的那份文本在**当下**可能与 Text() 逐字相同，
// 行为用例完全看不出来。本项目就是这么漏掉的 —— 自定义协议入口（/v1/systemone）写了
// 字面量 "mode=systemone;reason=direct"，格式看着没问题，直到 v0.72.0 上线选路画像后
// 才发现那个 mode 值根本不在 model.GroupMode 的枚举里（IsValid 与三处 binding oneof
// 都不认它），统计只能按未知值原样显示。手工拼的文本一旦与 Text() 分叉，两处都不会报错。
//
// 扫描范围是整个 internal/ 树而不只是本包：写 relay_logs 的路径也可以出现在 op、server 包。
func TestRelayLogDecisionIsNeverHandWritten(t *testing.T) {
	const root = ".." // internal/relay → internal
	fset := token.NewFileSet()
	scanned := 0
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// 跳过隐藏目录与测试数据，但根目录本身的名字是 ".."，不能一起跳掉
			// （否则整个守卫静默失效 —— 那比没有守卫更糟）。
			if path != root && (entry.Name() == "testdata" || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
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
		ast.Inspect(file, func(node ast.Node) bool {
			kv, ok := node.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "Decision" {
				return true
			}
			lit, ok := kv.Value.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			t.Errorf("%s: RelayLog.Decision 被赋了字符串字面量 %s —— 判定文本必须走 Decision.Text()"+
				"（或 SystemOneDecision() 这类受控构造器）；手工拼的副本在 Text() 格式变化时不会跟着变，两处都不报错",
				filepath.ToSlash(fset.Position(kv.Pos()).String()), lit.Value)
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("遍历 internal/ 失败: %v", walkErr)
	}
	// 守卫自身必须被验证：扫不到文件（路径写错、被跳过）时它会对一切放行，
	// 而「全绿」看起来和「没问题」一模一样。
	if scanned < 10 {
		t.Fatalf("只扫到 %d 个 .go 文件，守卫自身失效了（路径或跳过规则不对）", scanned)
	}
}
