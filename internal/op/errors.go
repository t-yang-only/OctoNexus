package op

import (
	"errors"
	"fmt"
)

// T-usability-012 「资源不存在」的可识别错误。
//
// ## 解决什么问题
//
// 审计发现：删除一个不存在的资源时接口返回 **500 服务器错误**：
//
//	DELETE /api/v1/apikey/delete/999999   → 500 "API key not found"
//	DELETE /api/v1/channel/delete/999999  → 500 "channel not found"
//	DELETE /api/v1/group/delete/999999    → 500 "group not found"
//
// 语义是错的：**500 表示"服务端出问题了，你可以重试"**，
// 而"这个资源不存在"是**确定的、不该重试的**结论。
// 调用方（含前端与外部工具）看到 500 会去重试或报"服务器错误"，
// 把一个明确的"东西没了"误报成故障。
//
// ## 为什么用哨兵错误而不是字符串匹配
//
// 可以在 handler 里判 `strings.Contains(err.Error(), "not found")` ——
// **但那正是本项目反复踩过的坑**（把 error 包成字符串会丢结构化信息）。
// 消息文案会改、会被包装、会被本地化，而哨兵错误不会。
//
// 用 `%w` 包装而不是直接返回哨兵：报给用户的消息要说明**什么**没找到
// （"API key 999999 not found"），只写 "resource not found"
// 用户不知道是哪一层的问题。而 errors.Is 会穿透 %w 包装正确识别。
var ErrNotFound = errors.New("resource not found")

// NotFoundf 构造一个「资源不存在」错误，带可读描述。
func NotFoundf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrNotFound, fmt.Sprintf(format, args...))
}

// IsNotFound 判断错误链里是否包含「资源不存在」。
//
// 用 errors.Is 而不是字符串匹配：调用方可能用 %w 包了好几层，
// 字符串匹配会漏；errors.Is 会正确穿透整条链。
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}
