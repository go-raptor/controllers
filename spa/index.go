package spa

import (
	"bytes"
	"compress/gzip"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
)

// representation is one encoding of a file, fully materialised in memory.
type representation struct {
	data []byte
	etag string
}

// entry is an indexed file: its bytes in every encoding we can serve, plus
// the headers that are identical on every request for it.
type entry struct {
	identity     representation
	gzip         *representation
	brotli       *representation
	contentType  string
	cacheControl string
	modTime      time.Time
}

func (e *entry) compressed() bool { return e.gzip != nil || e.brotli != nil }

func (e *entry) storedBytes() int64 {
	n := int64(len(e.identity.data))
	if e.gzip != nil {
		n += int64(len(e.gzip.data))
	}
	if e.brotli != nil {
		n += int64(len(e.brotli.data))
	}
	return n
}

// etagFor derives a representation's validator from the hash of the
// *original* content, so replicas agree even if their compressors do not.
func etagFor(base, encoding string) string {
	if encoding == "" {
		return `"` + base + `"`
	}
	return `"` + base + "-" + encoding + `"`
}

// Compression runs once at boot, so both codecs use their maximum level.
func compressGzip(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// compressBrotli returns nil rather than an error: brotli is a bonus
// encoding, and failing to produce it must not fail startup.
func compressBrotli(data []byte) []byte {
	var buf bytes.Buffer
	w := brotli.NewWriterLevel(&buf, brotli.BestCompression)
	if _, err := w.Write(data); err != nil {
		return nil
	}
	if err := w.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}

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
