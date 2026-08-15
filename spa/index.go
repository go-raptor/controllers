package spa

import (
	"mime"
	"net/http"
	"path/filepath"
	"strings"
)

// webTypes pins the types a frontend build actually contains.
// mime.TypeByExtension consults /etc/mime.types on Linux, so leaving these
// to the host makes responses differ between the machine that built the
// image and the one that runs it.
var webTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	".svg":         "image/svg+xml",
	".wasm":        "application/wasm",
	".woff":        "font/woff",
	".woff2":       "font/woff2",
	".ico":         "image/x-icon",
	".png":         "image/png",
	".jpg":         "image/jpeg",
	".jpeg":        "image/jpeg",
	".webp":        "image/webp",
	".avif":        "image/avif",
	".txt":         "text/plain; charset=utf-8",
	".xml":         "application/xml",
}

func contentTypeFor(path string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ct, ok := webTypes[ext]; ok {
		return ct
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return http.DetectContentType(data)
}

// isCompressible reports whether a type is worth compressing. Images, woff2
// and video are already compressed; running them through brotli burns boot
// time and memory to produce something larger.
func isCompressible(contentType string) bool {
	ct, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		ct = contentType
	}
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	if strings.HasSuffix(ct, "+json") || strings.HasSuffix(ct, "+xml") {
		return true
	}
	switch ct {
	case "application/json", "application/xml", "application/javascript",
		"application/wasm", "image/x-icon", "image/vnd.microsoft.icon":
		return true
	}
	return false
}
