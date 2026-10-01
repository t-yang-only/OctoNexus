package op

import (
	"errors"
	"fmt"
	"testing"
)

// T-usability-012 「资源不存在」哨兵错误的判据。
//
// ## 为什么值得单独测一层错误类型
//
// 它的作用是让 handler 能把「资源不存在」映射成 404 而不是 500。
// 一旦 errors.Is 的穿透能力坏掉（比如有人把 %w 改成 %v），
// 整条 404 映射会**静默失效** —— 接口回到 500，而没有任何测试会红。
// 这就是这条判据存在的理由。

// 哨兵错误本身要能被识别。
func TestIsNotFoundOnSentinel(t *testing.T) {
	if !IsNotFound(ErrNotFound) {
		t.Fatalf("ErrNotFound 本身应被 IsNotFound 识别")
	}
}

// **关键**：经 %w 包装后仍需识别。
//
// 这是实际用法 —— NotFoundf 内部就是 fmt.Errorf("%w: ...")。
// 如果包装方式被改成 %v，errors.Is 的链会断，404 映射静默失效。
func TestIsNotFoundThroughWrapping(t *testing.T) {
	err := NotFoundf("API key %d not found", 999)
	if !IsNotFound(err) {
		t.Fatalf("NotFoundf 产生的错误应被识别（它内部用 %%w 包装了哨兵）")
	}
	// 消息里要说明**什么**没找到，否则用户不知道是哪一层的问题。
	if got := err.Error(); got == "resource not found" {
		t.Fatalf("消息应带具体对象（如 \"API key 999999 not found\"），实得 %q", got)
	}
}

// 多层包装也要穿透 —— handler 可能把 op 的错误再包一层。
func TestIsNotFoundThroughMultipleLayers(t *testing.T) {
	inner := NotFoundf("group %d not found", 42)
	outer := fmt.Errorf("failed to delete group: %w", inner)
	if !IsNotFound(outer) {
		t.Fatalf("多层 %%w 包装后仍应被识别，否则 handler 会回 500")
	}
}

// **反向对照**：普通错误不能被误判成「资源不存在」——
// 否则真故障会被报成 404，用户以为"东西没了"而实际是服务端坏了。
func TestIsNotFoundRejectsOtherErrors(t *testing.T) {
	others := []error{
		nil,
		errors.New("database is locked"),
		fmt.Errorf("failed to delete channel: %w", errors.New("disk full")),
		ErrNotFound, // 哨兵本身算 true，下面单独判断
	}
	for _, err := range others[:3] {
		if IsNotFound(err) {
			t.Fatalf("普通错误不该被识别成「资源不存在」：%v", err)
		}
	}
	// nil 必须安全（handler 里可能直接传 nil）。
	if IsNotFound(nil) {
		t.Fatalf("nil 不该被识别成「资源不存在」")
	}
}

// 文案里带参数时不能丢参数 —— 用户要知道是哪个 id 没找到。
func TestNotFoundfKeepsArguments(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{NotFoundf("API key %d not found", 999), "999"},
		{NotFoundf("group %d not found", 42), "42"},
		{NotFoundf("channel %d not found", 7), "7"},
	}
	for _, c := range cases {
		if !IsNotFound(c.err) {
			t.Fatalf("应被识别为未找到：%v", c.err)
		}
		msg := c.err.Error()
		found := false
		for i := 0; i+len(c.want) <= len(msg); i++ {
			if msg[i:i+len(c.want)] == c.want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("消息应含 id %s，实得 %q", c.want, msg)
		}
	}
}
