package task

import (
	"testing"

	"github.com/bestruirui/octopus/internal/op"
)

// T-proxy-002 「渠道被坏节点挡住」的通知去重。
//
// 去重是这个功能的成败关键：节点 5 分钟探一轮，不去重就会每 5 分钟一条骚扰，
// 结果必然是用户把通知关掉 —— 那比不通知更糟。所以判据集中在指纹上：
// 集合没变必须判定为"无需通知"，集合变化必须判定为"需要通知"。

func blockers(pairs ...[2]interface{}) []op.ProxyChannelBlocker {
	out := make([]op.ProxyChannelBlocker, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, op.ProxyChannelBlocker{
			ChannelID:   p[0].(int),
			NodeID:      p[1].(int),
			ChannelName: "ch",
			NodeName:    "node",
		})
	}
	return out
}

// 空集合的指纹为空串，与"上次是正常"用同一个值表示。
func TestProxyBlockerFingerprintEmpty(t *testing.T) {
	if got := proxyBlockerFingerprint(nil); got != "" {
		t.Fatalf("空集合的指纹应为空串，实得 %q", got)
	}
	if got := proxyBlockerFingerprint([]op.ProxyChannelBlocker{}); got != "" {
		t.Fatalf("空切片同样应为空串，实得 %q", got)
	}
}

// 同一个集合无论顺序如何，指纹必须一致（否则会因顺序抖动而反复通知）。
func TestProxyBlockerFingerprintOrderInsensitive(t *testing.T) {
	a := proxyBlockerFingerprint(blockers([2]interface{}{1, 10}, [2]interface{}{2, 20}, [2]interface{}{3, 30}))
	b := proxyBlockerFingerprint(blockers([2]interface{}{3, 30}, [2]interface{}{1, 10}, [2]interface{}{2, 20}))
	if a != b {
		t.Fatalf("同一集合的不同顺序应得到同一指纹：\n  a=%q\n  b=%q", a, b)
	}
	if a == "" {
		t.Fatalf("非空集合的指纹不该为空串")
	}
}

// 集合变化必须让指纹变化（否则真出问题时反而不通知）。
func TestProxyBlockerFingerprintChanges(t *testing.T) {
	base := proxyBlockerFingerprint(blockers([2]interface{}{1, 10}))
	cases := map[string][]op.ProxyChannelBlocker{
		"多一个渠道":  blockers([2]interface{}{1, 10}, [2]interface{}{2, 20}),
		"换了节点":   blockers([2]interface{}{1, 11}),
		"换成别的渠道": blockers([2]interface{}{2, 10}),
	}
	for name, items := range cases {
		if got := proxyBlockerFingerprint(items); got == base {
			t.Fatalf("%s 时指纹应变化，实得与基准相同 %q", name, got)
		}
	}
}

// 指纹不含错误文案：节点错误偶尔在"超时/EOF"之间抖动，算进去会反复通知。
func TestProxyBlockerFingerprintIgnoresErrorText(t *testing.T) {
	a := []op.ProxyChannelBlocker{{ChannelID: 1, NodeID: 10, NodeError: "EOF"}}
	b := []op.ProxyChannelBlocker{{ChannelID: 1, NodeID: 10, NodeError: "i/o timeout"}}
	if proxyBlockerFingerprint(a) != proxyBlockerFingerprint(b) {
		t.Fatalf("错误文案变化不该改变指纹（否则集合没变也会反复通知）")
	}
}

// 恢复：从"有问题"回到"没问题"时，指纹必须从非空变为空。
func TestProxyBlockerFingerprintRecovery(t *testing.T) {
	before := proxyBlockerFingerprint(blockers([2]interface{}{1, 10}))
	after := proxyBlockerFingerprint(nil)
	if before == "" || after != "" {
		t.Fatalf("恢复前后的指纹应分别是非空与空，实得 before=%q after=%q", before, after)
	}
	if before == after {
		t.Fatalf("恢复必须被识别为「集合变化」，否则不会发出恢复通知")
	}
}

// 指纹是纯函数：同样的输入永远同样的输出（去重逻辑依赖这一点）。
func TestProxyBlockerFingerprintDeterministic(t *testing.T) {
	items := blockers([2]interface{}{5, 50}, [2]interface{}{6, 60})
	first := proxyBlockerFingerprint(items)
	for i := 0; i < 10; i++ {
		if got := proxyBlockerFingerprint(items); got != first {
			t.Fatalf("第 %d 次调用结果不同：%q vs %q", i, got, first)
		}
	}
}
