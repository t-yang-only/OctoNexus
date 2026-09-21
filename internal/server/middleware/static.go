package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/gin-gonic/gin"
)

func StaticEmbed(urlPrefix string, embedFS fs.FS) gin.HandlerFunc {
	return static(urlPrefix, http.FS(embedFS))
}

func StaticLocal(urlPrefix string, localPath string) gin.HandlerFunc {
	return static(urlPrefix, http.Dir(localPath))
}

func openStatic(fileSystem http.FileSystem, name string) http.File {
	file, err := fileSystem.Open(name)
	if err != nil {
		return nil
	}
	stat, err := file.Stat()
	if err != nil || stat.IsDir() {
		file.Close()
		return nil
	}
	return file
}

// spaFallbackEligible 判断这次请求是否该回落到 SPA 入口（index.html）。
//
// 判据刻意保守：只有"像是页面路由"的请求才兜底 ——
//   - 只认 GET/HEAD；
//   - 路径没有扩展名（/channel 兜底，/assets/index.js 或 /favicon.ico 不兜底，
//     否则前端资源真的缺失时会被一个 HTML 页面顶替，问题被藏起来）；
//   - 不是 /api 与 /v1（接口路径必须保持真实 404，客户端要能看见）；
//   - 客户端接受 HTML（curl 直接拉接口时不该收到一坨 HTML）。
func spaFallbackEligible(c *gin.Context) bool {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		return false
	}
	if path.Ext(c.Request.URL.Path) != "" {
		return false
	}
	// 判据必须是 /api/ 而不是 /api —— 前缀匹配会把面板自己的页面路径也误判成接口：
	// 实测 /apikey（「API Key」页）因此 404，而那正是用户报障时在的那一页。
	if c.Request.URL.Path == "/api" || strings.HasPrefix(c.Request.URL.Path, "/api/") ||
		c.Request.URL.Path == "/v1" || strings.HasPrefix(c.Request.URL.Path, "/v1/") {
		return false
	}
	accept := c.GetHeader("Accept")
	return accept == "" || strings.Contains(accept, "text/html") || strings.Contains(accept, "*/*")
}

func static(urlPrefix string, fileSystem http.FileSystem) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 同一处前缀陷阱：这里也必须认 /api/ 而不是 /api，否则 /apikey 这类页面路径
		// 会被整段跳过静态处理，直接落到 gin 的 404。
		if c.Request.URL.Path == "/api" || strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Next()
			return
		}
		name := path.Clean("/" + strings.TrimPrefix(c.Request.URL.Path, urlPrefix))
		if name == "/" {
			name = "/index.html"
		}

		acceptsGzip := strings.Contains(c.GetHeader("Accept-Encoding"), "gzip")

		var file http.File
		var encoded, inflate bool
		if acceptsGzip {
			if file = openStatic(fileSystem, name+".gz"); file != nil {
				encoded = true
			}
		}
		if file == nil {
			file = openStatic(fileSystem, name)
		}
		if file == nil && !acceptsGzip {
			if file = openStatic(fileSystem, name+".gz"); file != nil {
				inflate = true
			}
		}
		if file == nil {
			// SPA 深链兜底：面板是客户端路由（/channel、/group、/setting…），这些路径没有对应的
			// 静态文件。原来这里直接 c.Next()，于是落到 gin 的默认 404 ——
			// 用户在浏览器里刷新任意子页面，看到的就是一行 "404 page not found"。
			// 只在"看起来是页面路由"时兜底，其余（缺资源、API 路径）照旧 404，
			// 否则会把真正缺失的脚本/接口也伪装成正常页面。
			if !spaFallbackEligible(c) {
				c.Next()
				return
			}
			name = "/index.html"
			if acceptsGzip {
				if file = openStatic(fileSystem, name+".gz"); file != nil {
					encoded = true
				}
			}
			if file == nil {
				file = openStatic(fileSystem, name)
			}
			// 前端产物只有 .gz（构建时压缩），所以"客户端不收 gzip"这一支必须自己解压 ——
			// 少了这一步，curl（不带 Accept-Encoding）拿深链会 404，而浏览器却正常，
			// 是最容易漏测的一种形态（本轮实测踩过）。
			if file == nil && !acceptsGzip {
				if file = openStatic(fileSystem, name+".gz"); file != nil {
					inflate = true
				}
			}
			if file == nil {
				c.Next()
				return
			}
		}
		defer file.Close()

		if strings.HasPrefix(name, "/assets/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			c.Header("Cache-Control", "no-cache")
		}
		if encoded || inflate {
			c.Header("Vary", "Accept-Encoding")
		}

		var content io.ReadSeeker = file
		switch {
		case encoded:
			c.Header("Content-Encoding", "gzip")
			if ctype := mime.TypeByExtension(path.Ext(name)); ctype != "" {
				c.Header("Content-Type", ctype)
			}
		case inflate:
			reader, err := gzip.NewReader(file)
			if err != nil {
				resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
				c.Abort()
				return
			}
			defer reader.Close()
			raw, err := io.ReadAll(reader)
			if err != nil {
				resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
				c.Abort()
				return
			}
			content = bytes.NewReader(raw)
		}
		http.ServeContent(c.Writer, c.Request, name, time.Time{}, content)
		c.Abort()
	}
}
