package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// 插件是被 octopus **拉起来的可执行体**，清单校验因此必须 fail-closed：
// 宽松的清单等于把"我信任这个目录"写在脸上。这组用例逐个钉住拒绝条件。
func TestLoadRejectsUnsafeManifests(t *testing.T) {
	cases := []struct {
		name     string
		dirName  string
		manifest string
		entry    bool // 是否真的在目录里创建入口文件
		wantSub  string
	}{
		{
			name: "入口绝对路径", dirName: "abs-entry",
			manifest: `{"name":"x","runtime":"exec","entry":"C:\\Windows\\System32\\cmd.exe"}`,
			wantSub:  "绝对路径",
		},
		{
			name: "入口目录穿越", dirName: "traverse",
			manifest: `{"name":"x","runtime":"exec","entry":"../../evil.sh"}`,
			wantSub:  "..",
		},
		{
			name: "入口必须存在", dirName: "missing-entry",
			manifest: `{"name":"x","runtime":"exec","entry":"run.py"}`,
			wantSub:  "不存在",
		},
		{
			name: "未知 runtime", dirName: "bad-runtime",
			manifest: `{"name":"x","runtime":"docker","entry":"run.py"}`,
			wantSub:  "runtime",
		},
		{
			name: "slug 与目录名不一致", dirName: "slug-mismatch",
			manifest: `{"slug":"someone-else","name":"x","runtime":"http","entry":"http://127.0.0.1:9/v1"}`,
			wantSub:  "不一致",
		},
		{
			name: "http 入口必须是地址", dirName: "http-bad",
			manifest: `{"name":"x","runtime":"http","entry":"127.0.0.1:8080"}`,
			wantSub:  "http://",
		},
		{
			name: "http 不能声明 pool 出口", dirName: "http-external",
			manifest: `{"name":"x","runtime":"http","entry":"http://127.0.0.1:9/v1","egress":"external","entry_ok":true}`,
			wantSub:  "",
		},
		{
			name: "目录名非法", dirName: "Bad Slug",
			manifest: `{"name":"x","runtime":"http","entry":"http://127.0.0.1:9/v1"}`,
			wantSub:  "不是合法标识",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, tc.dirName)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(tc.manifest), 0o644); err != nil {
				t.Fatalf("write manifest: %v", err)
			}
			if tc.entry {
				if err := os.WriteFile(filepath.Join(dir, "run.py"), []byte("print(1)"), 0o644); err != nil {
					t.Fatalf("write entry: %v", err)
				}
			}
			parsed, err := Load(dir)
			if tc.wantSub == "" {
				// 这一例是"合法但需提示"：external 出口只对 runtime=http 有意义。
				if err != nil {
					t.Fatalf("应当接受：%v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("应当拒绝（%s），却通过了：%+v", tc.wantSub, parsed)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("错误文案 = %q, want 含 %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestLoadNormalizesEntryAndDefaults(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "my-relay")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.py"), []byte("print(1)"), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	manifest := `{"name":"我的反代","entry":"./sub/../run.py","args":["--port","{port}"]}`
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	parsed, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if parsed.Runtime != RuntimeExec {
		t.Fatalf("runtime 默认应为 exec，得到 %q", parsed.Runtime)
	}
	if parsed.Entry != "run.py" {
		t.Fatalf("entry 归一化 = %q, want run.py", parsed.Entry)
	}
	if parsed.EgressMode != EgressPool {
		t.Fatalf("egress 默认应为 pool（强制走节点出口），得到 %q", parsed.EgressMode)
	}
	if parsed.PortEnv != "PORT" || parsed.BasePath != "/v1" || parsed.HealthPath != "/v1/models" {
		t.Fatalf("默认值不对：%+v", parsed.Manifest)
	}
}

func TestRenderArgsRejectsUnknownPlaceholder(t *testing.T) {
	rendered, err := RenderArgs([]string{"--port", "{port}", "--dir", "{dir}"}, 42001, "/tmp/p", "/tmp", "p")
	if err != nil {
		t.Fatalf("RenderArgs: %v", err)
	}
	if rendered[1] != "42001" || rendered[3] != "/tmp/p" {
		t.Fatalf("rendered = %v", rendered)
	}
	// 未知占位符必须报错：原样传给插件会变成"起来了但行为不对"，比直接拒绝难查得多。
	if _, err := RenderArgs([]string{"--port", "{post}"}, 42001, "/tmp/p", "/tmp", "p"); err == nil {
		t.Fatal("未知占位符应当被拒绝")
	}
}

// TestBuildEnvInjectsEgressAndKeepsLoopbackOut 是"全局隐秘代理"这一承诺的判据：
// 插件不需要任何代码改动，标准库发请求就会走节点出口；而回环必须绕过代理
// （否则插件回调 octopus 或访问自己都会被送到出口节点上解析）。
func TestBuildEnvInjectsEgressAndKeepsLoopbackOut(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://stale.example:1") // 上一个进程留下的残留必须被清掉
	row := model.Plugin{Slug: "relay-demo", PortEnv: "PORT", Protocol: ProtocolOpenAI, BasePath: "/v1"}
	env := BuildEnv(row, 42007, "http://127.0.0.1:41000", "sk-plugin-token")

	lookup := map[string]string{}
	for _, kv := range env {
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			lookup[strings.ToUpper(kv[:idx])] = kv[idx+1:]
		}
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "OCTOPUS_EGRESS_PROXY"} {
		if lookup[key] != "http://127.0.0.1:41000" {
			t.Fatalf("%s = %q, want 出口地址（残留 %q 未被覆盖）", key, lookup[key], lookup["HTTP_PROXY"])
		}
	}
	// 端口变量名来自清单（默认 PORT），插件据此决定监听哪个端口。
	if lookup["PORT"] != "42007" {
		t.Fatalf("PORT = %q, want 42007", lookup["PORT"])
	}
	if lookup["OCTOPUS_PLUGIN_TOKEN"] != "sk-plugin-token" {
		t.Fatalf("插件令牌没注入：%q", lookup["OCTOPUS_PLUGIN_TOKEN"])
	}
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		if !strings.Contains(lookup[strings.ToUpper(key)], "127.0.0.1") {
			t.Fatalf("%s = %q, want 含回环（否则插件回调自己会绕出口）", key, lookup[strings.ToUpper(key)])
		}
	}
	if lookup["OCTOPUS_PLUGIN_PORT"] != "42007" {
		t.Fatalf("OCTOPUS_PLUGIN_PORT = %q", lookup["OCTOPUS_PLUGIN_PORT"])
	}
}

// TestResolveEgressFailsClosed 出口拿不到时**必须拒绝启动**，绝不退回直连：
// 让插件用真实 IP 出去，恰好毁掉这个功能存在的意义。
func TestResolveEgressFailsClosed(t *testing.T) {
	row := model.Plugin{Slug: "no-egress", EgressMode: EgressPool}
	if _, _, err := ResolveEgress(row); err == nil {
		t.Fatal("没有出口节点时应当拒绝")
	} else if !strings.Contains(err.Error(), "没有出口节点") {
		t.Fatalf("错误文案应说明原因：%v", err)
	}

	direct := model.Plugin{Slug: "explicit-direct", EgressMode: EgressDirect}
	proxy, _, err := ResolveEgress(direct)
	if err != nil {
		t.Fatalf("显式声明 direct 时不应报错：%v", err)
	}
	if proxy != "" {
		t.Fatalf("direct 模式不该注入出口，得到 %q", proxy)
	}
}
