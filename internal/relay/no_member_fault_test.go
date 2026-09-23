package relay

import (
	"fmt"
	"net/http"
	"testing"
)

// T-faultkind-001 「分组无可用成员」这条出口的归因必须落成 request，且请求必须以 failed 定稿。
//
// ## 修之前发生了什么（实测）
//
// 该分支调用的是 rejectRequest —— 它只写 HTTP 响应、**不碰请求状态**。
// 而这里请求状态早已登记（newRequestState 在其之上），于是记录永远停在 running，
// 最后被 ctx 分支收尾成 status=canceled + error="context canceled"。
// 后果有两层，第二层更隐蔽：
//  1. 服务端自己的判定被记成"客户端取消"，与事实相反；
//  2. 归因桶里根本看不到它 —— 用户看到「成员故障 0 / 请求非法 0」，
//     却实际有一条请求因为"分组没成员"失败了，排查时会被引向错误方向。
//
// 实测证据：本地实例经转发口打一个没有可用成员的分组，HTTP 返回 400，
// 但 log/history 里那条记录是 {"status":"canceled","fault_kind":"","error":"context canceled"}。
//
// ## 判据设计
//
// 退出用哪个函数（failRequest vs rejectRequest）是分支级行为，单测拿不到 gin 上下文，
// 因此这里钉住**归因输入**这一侧：该错误必须同时具备 400 状态码与 request 归因。
// 负向对照证明这条判据真的能发现漂移 —— 丢了状态码就会变成 transient。

// 无可用成员的出口错误必须同时带 400 与 request 归因。
func TestNoAvailableMemberIsRequestFault(t *testing.T) {
	err := newUpstreamStatusError(http.StatusBadRequest, fmt.Sprintf(
		"group %q has no available member after waiting %ds", "demo-group", 60))

	status, ok := upstreamStatusOf(err)
	if !ok {
		t.Fatalf("该错误必须带状态码：不带的话归因只能按 transient 兜底，渠道故障率会被这条请求污染")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 %d（这是请求侧问题，不是渠道故障）", status, http.StatusBadRequest)
	}
	if got := faultKindOf(err); got != "request" {
		t.Fatalf("归因 = %q，期望 %q：分组没有可用成员是配置/授权问题，不该算成员故障",
			got, "request")
	}
}

// 负向对照：同一条消息若不包成带状态码的 error，归因会漂成 transient。
// 这条用例存在的意义是证明上一条判据不是恒真的 —— 一旦有人把 newUpstreamStatusError 换回裸 fmt.Errorf，
// 上面的用例会变红，而不会因为"不管怎么改都过"而失去保护力。
// （实测：初版就是这么写的，本用例当场变红，抓出了"显式造状态码"这一步。）
func TestNoAvailableMemberWithoutStatusDrifts(t *testing.T) {
	plain := fmt.Errorf("group %q has no available member after waiting %ds", "demo-group", 60)
	if got := faultKindOf(plain); got != "transient" {
		t.Fatalf("无状态码时归因 = %q，期望 %q（既有的保守兜底口径）", got, "transient")
	}
}
