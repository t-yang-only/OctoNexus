package relay

import (
	"errors"
	"testing"
)

// T-usability-007 失败归因分类的判据。
//
// ## 为什么需要这个分类
//
// 通过率是个会骗人的指标。实测：senseaudio 一度显示通过率 31.6%，
// 看起来像渠道坏了 —— 实际上那 25 次失败全是「用 chat 接口去调 TTS/图像等专用模型」
// 造成的**请求非法**，跟渠道一点关系都没有。
//
// 把这两类混在一起算，用户会去修一个根本没坏的东西。
//
// 所以分类的判据要盯住一个关键不变量：
// **「换任何成员都会同样失败」的错误绝不能算成渠道故障**。

// 请求本身非法 → request（不计入渠道通过率）。
func TestFaultKindRequestFault(t *testing.T) {
	// 400/405/406/413/414/415/422/501：任何成员都会同样拒绝。
	for _, status := range []int{400, 405, 406, 413, 414, 415, 422, 501} {
		got := faultKindOf(newUpstreamStatusError(status, "upstream rejected"))
		if got != "request" {
			t.Fatalf("HTTP %d 属请求本身非法，应归为 request（不该算渠道故障），实得 %q", status, got)
		}
	}
}

// 成员自身问题 → member（算渠道故障，换人可能成功）。
func TestFaultKindMemberFault(t *testing.T) {
	for _, status := range []int{401, 402, 403, 404, 407} {
		got := faultKindOf(newUpstreamStatusError(status, "auth failed"))
		if got != "member" {
			t.Fatalf("HTTP %d 属成员自身问题，应归为 member，实得 %q", status, got)
		}
	}
}

// 可恢复错误 → transient（算渠道故障）。
func TestFaultKindTransient(t *testing.T) {
	for _, status := range []int{408, 409, 429, 500, 502, 503, 504} {
		got := faultKindOf(newUpstreamStatusError(status, "server error"))
		if got != "transient" {
			t.Fatalf("HTTP %d 属可恢复，应归为 transient，实得 %q", status, got)
		}
	}
}

// 取不到状态码（网络错误/超时）按 transient 处理 —— 与既有失败处置口径一致。
//
// 这条守的是一个跨模块的一致性：faultKindOf 内部复用 classifyUpstreamFailure，
// 如果哪天有人在这里另写一套判断，统计口径就会和重试行为分叉。
func TestFaultKindNetworkErrorIsTransient(t *testing.T) {
	got := faultKindOf(errors.New("dial tcp: connection refused"))
	if got != "transient" {
		t.Fatalf("无状态码的网络错误应归为 transient（与重试口径一致），实得 %q", got)
	}
}

// 分类必须与失败处置同源：同一批状态码在两边必须落在同一类。
//
// 这是防分叉的判据 —— 分开写两套 if-else 是这类代码最常见的腐化方式：
// 两边单独看都对，合起来就矛盾。
func TestFaultKindMatchesDisposition(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{400, "request"},
		{401, "member"},
		{429, "transient"},
	}
	for _, c := range cases {
		err := newUpstreamStatusError(c.status, "x")
		if got := faultKindOf(err); got != c.want {
			t.Fatalf("HTTP %d: faultKindOf=%q, want %q", c.status, got, c.want)
		}
		// 同一状态码在处置侧的结果必须能对上分类。
		disposition := classifyUpstreamFailure(err)
		var expectDisposition failureDisposition
		switch c.want {
		case "request":
			expectDisposition = dispositionRequestFault
		case "member":
			expectDisposition = dispositionMemberFault
		default:
			expectDisposition = dispositionRetry
		}
		if disposition != expectDisposition {
			t.Fatalf("HTTP %d: 分类为 %q 但处置为 %v，两处口径已分叉",
				c.status, c.want, disposition)
		}
	}
}
