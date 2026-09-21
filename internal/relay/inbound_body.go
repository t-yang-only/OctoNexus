// 入站请求体的读取与上限（T-bodylimit-001）。
//
// 为什么要有这个文件:
// 我们原本直接用依赖库的 httpclient.ReadHTTPRequest 读取客户端正文, 而它把上限写死成一个**包内私有常量**
// （axonhub/llm 的 maxRequestBodySize = 64 MiB, 外部既读不到也改不了）。超过 64 MiB 的正文一律被拒,
// 回给客户端的是 "failed to read request body: request body too large" —— Codex 的 remote compact
// 这类"把整段会话压过来"的请求正好会踩到, 而且是用户自己人的请求先被自家网关挡掉, 排查时很容易误判成上游问题。
//
// 本文件按下面三条口径替换它:
//  1. 上限由设置项 relay_max_request_body_bytes 决定（默认 256 MiB, 0 = 不限制）, 改完热生效;
//  2. 除上限外的语义与依赖库**逐字对齐**：同样的字段填充、同样的内容编码解码（identity/gzip/deflate/zstd）、
//     解码后同样删掉 Content-Encoding 与 Content-Length、同样的 getClientIP 取值顺序;
//  3. 超限仍然返回依赖库的哨兵错误 ErrRequestBodyTooLarge, 于是"是不是超限"这件事只有一种判定方式,
//     调用方（handler）用 errors.Is 就能把它翻译成明确的 413, 而不是一句笼统的 400。
//
// 内存口径（必须知道）: 正文会整份读进内存（axоnhub 原实现同样如此），并作为字符串登记进请求状态、
// 每轮改写时再复制一份。上限开得越大, 单请求峰值内存越高 —— 这是"能不能收大请求"与"进程内存"之间的取舍开关。
package relay

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/klauspost/compress/zstd"
	"github.com/looplj/axonhub/llm/httpclient"
)

// defaultInboundBodyBytes 与设置项的默认值同源（256 MiB）: 设置项缺失时（例如老库还没补行）用它兜底,
// 而不是退回依赖库那 64 MiB —— 退回旧上限等于这次修复在升级路径上失效。
const defaultInboundBodyBytes = 268435456

// inboundBodyLimitOverride 只给单元测试用: 0 = 走设置项; 正数 = 直接用这个上限; 负数 = 显式不限制。
// 之所以需要它, 是因为上限的判据必须能测"超限被拒"与"不限制放行"两种情况, 而单元测试里没有数据库可读设置。
var inboundBodyLimitOverride int64

// inboundBodyLimit 取当前生效的入站正文上限（字节）。0 表示不限制。
//
// 只有设置项这一个来源（不另设环境变量）: 上限是"能不能收大请求"的显式取舍, 两处来源迟早出现
// "面板显示 256 MiB 而实际按 64 MiB 拦"的排查陷阱。设置项本身热生效、面板可改, 够用。
func inboundBodyLimit() int64 {
	if inboundBodyLimitOverride != 0 {
		if inboundBodyLimitOverride < 0 {
			return 0
		}
		return inboundBodyLimitOverride
	}
	value, err := op.SettingGetInt(model.SettingKeyRelayMaxRequestBody)
	if err != nil || value < 0 {
		return defaultInboundBodyBytes
	}
	return int64(value)
}

// readLimitedWith 与依赖库的 readLimited 同语义: 多读一个字节用来判定"超了", 超限返回 ErrRequestBodyTooLarge。
func readLimitedWith(r io.Reader, maxSize int64) ([]byte, error) {
	if maxSize <= 0 {
		body, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		return body, nil
	}
	body, err := io.ReadAll(io.LimitReader(r, maxSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxSize {
		return nil, httpclient.ErrRequestBodyTooLarge
	}
	return body, nil
}

// readInboundRequest 读取客户端请求, 返回依赖库的 Request 结构（上限由我们控制）。
func readInboundRequest(rawReq *http.Request) (*httpclient.Request, error) {
	limit := inboundBodyLimit()
	request := &httpclient.Request{
		Method:     rawReq.Method,
		URL:        rawReq.URL.String(),
		Path:       rawReq.URL.Path,
		Query:      rawReq.URL.Query(),
		Headers:    rawReq.Header,
		Body:       nil,
		Auth:       &httpclient.AuthConfig{},
		RequestID:  "",
		ClientIP:   inboundClientIP(rawReq),
		RawRequest: rawReq,
	}

	body, err := readLimitedWith(rawReq.Body, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	if len(body) > 0 {
		body, err = decodeInboundBody(body, request.Headers, limit)
		if err != nil {
			return nil, err
		}
	}

	request.Body = body

	return request, nil
}

// decodeInboundBody 与依赖库的 decodeRequestBody 同语义（含它支持的四种编码与两处头删除）。
func decodeInboundBody(body []byte, headers http.Header, maxSize int64) ([]byte, error) {
	contentEncoding := headers.Get("Content-Encoding")
	if contentEncoding == "" {
		return body, nil
	}

	encoding := strings.ToLower(strings.TrimSpace(contentEncoding))

	switch encoding {
	case "identity", "":
		return body, nil

	case "gzip", "x-gzip":
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer reader.Close()

		decoded, err := readLimitedWith(reader, maxSize)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress gzip body: %w", err)
		}

		headers.Del("Content-Encoding")
		headers.Del("Content-Length")

		return decoded, nil

	case "deflate":
		decoded, err := decodeZlibOrFlateWith(body, maxSize)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress deflate body: %w", err)
		}

		headers.Del("Content-Encoding")
		headers.Del("Content-Length")

		return decoded, nil

	case "zstd":
		decoder, err := zstd.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
		}
		defer decoder.Close()

		decoderReader := decoder.IOReadCloser()
		decoded, err := readLimitedWith(decoderReader, maxSize)
		closeErr := decoderReader.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to decode zstd compressed body: %w", err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("failed to close zstd decoder: %w", closeErr)
		}

		headers.Del("Content-Encoding")
		headers.Del("Content-Length")

		return decoded, nil

	default:
		return nil, fmt.Errorf("unsupported content encoding: %s", contentEncoding)
	}
}

// decodeZlibOrFlateWith: RFC 7230 说 deflate 是 zlib, 但不少客户端发裸 DEFLATE, 先试 zlib 再退回裸流。
func decodeZlibOrFlateWith(body []byte, maxSize int64) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(body))
	if err == nil {
		defer reader.Close()
		return readLimitedWith(reader, maxSize)
	}

	flateReader := flate.NewReader(bytes.NewReader(body))
	defer flateReader.Close()
	return readLimitedWith(flateReader, maxSize)
}

// inboundClientIP 与依赖库的 getClientIP 同序: X-Forwarded-For 第一跳 → X-Real-IP → RemoteAddr 的 host。
func inboundClientIP(req *http.Request) string {
	if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
		if before, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(before)
		}
		return xff
	}

	if xri := req.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	if ip, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		return ip
	}

	return req.RemoteAddr
}
