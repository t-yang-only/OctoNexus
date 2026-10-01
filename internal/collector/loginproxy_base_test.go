package collector

import (
	"strings"
	"testing"
)

// TestRewriteHTMLInjectsBase 钉住 2026-09-20 用户实测的"白屏"第二个原因：
//
// 属性改写只覆盖 href/src/action，而 SPA 构建产物里还有**根相对**的引用
// （Vite 的动态 import /assets/chunk-x.js、CSS 里的绝对路径），它们不受属性改写影响，
// 在浏览器里会解析到面板自己的根 → 404 → 白屏。
// 注入 <base href="<prefix>/"> 后这些引用全部落回反代前缀下。
func TestRewriteHTMLInjectsBase(t *testing.T) {
	proxy, err := NewLoginProxy("https://api.example.com", NewSession(), "/api/v1/collector/portal/9/tok123")
	if err != nil {
		t.Fatalf("建反代失败：%v", err)
	}
	page := `<html><head><meta charset="utf-8"><title>登录</title></head><body><div id="root"></div><script type="module" crossorigin src="/assets/index-abc.js"></script></body></html>`
	got := string(proxy.rewriteHTML([]byte(page)))

	if !strings.Contains(got, `<base href="/api/v1/collector/portal/9/tok123/">`) {
		t.Fatalf("必须注入 base 指向反代前缀，实得：%s", got)
	}
	// base 必须在 <head> 之内、且在任何子资源引用之前
	if strings.Index(got, "<base ") > strings.Index(got, "/assets/index-abc.js") {
		t.Fatalf("base 必须出现在子资源引用之前，实得：%s", got)
	}
	// 属性改写照旧生效（与 base 叠加不冲突：绝对路径不受 base 影响，两者指向同一处）
	if !strings.Contains(got, `src="/api/v1/collector/portal/9/tok123/assets/index-abc.js"`) {
		t.Fatalf("脚本地址应被改写进前缀，实得：%s", got)
	}
	// 幂等：已有 base 不再重复注入
	again := string(proxy.rewriteHTML([]byte(got)))
	if strings.Count(again, "<base ") != 1 {
		t.Fatalf("重复改写不得叠加 base，实得 %d 个", strings.Count(again, "<base "))
	}
}
