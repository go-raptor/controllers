package spa

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/andybalholm/brotli"
)

func TestContentTypeFor(t *testing.T) {
	tests := []struct {
		path string
		data []byte
		want string
	}{
		{"app.js", nil, "text/javascript; charset=utf-8"},
		{"app.mjs", nil, "text/javascript; charset=utf-8"},
		{"style.CSS", nil, "text/css; charset=utf-8"},
		{"index.html", nil, "text/html; charset=utf-8"},
		{"data.json", nil, "application/json"},
		{"app.js.map", nil, "application/json"},
		{"logo.svg", nil, "image/svg+xml"},
		{"font.woff2", nil, "font/woff2"},
		{"mod.wasm", nil, "application/wasm"},
		{"site.webmanifest", nil, "application/manifest+json"},
		{"noextension", []byte("plain text here"), "text/plain; charset=utf-8"},
	}

	for _, tt := range tests {
		if got := contentTypeFor(tt.path, tt.data); got != tt.want {
			t.Errorf("contentTypeFor(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestIsCompressible(t *testing.T) {
	tests := []struct {
		contentType string
		want        bool
	}{
		{"text/html; charset=utf-8", true},
		{"text/javascript; charset=utf-8", true},
		{"text/css; charset=utf-8", true},
		{"application/json", true},
		{"image/svg+xml", true},
		{"application/manifest+json", true},
		{"application/wasm", true},
		{"image/png", false},
		{"image/webp", false},
		{"image/avif", false},
		{"font/woff2", false},
		{"video/mp4", false},
		{"application/octet-stream", false},
	}

	for _, tt := range tests {
		if got := isCompressible(tt.contentType); got != tt.want {
			t.Errorf("isCompressible(%q) = %v, want %v", tt.contentType, got, tt.want)
		}
	}
}

func TestCompressGzipRoundTrips(t *testing.T) {
	original := []byte(jsSource)

	compressed, err := compressGzip(original)
	if err != nil {
		t.Fatalf("compressGzip: %v", err)
	}
	if len(compressed) >= len(original) {
		t.Fatalf("gzip did not shrink input: %d >= %d", len(compressed), len(original))
	}

	r, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatal("gzip round-trip changed the bytes")
	}
}

func TestCompressBrotliRoundTrips(t *testing.T) {
	original := []byte(jsSource)

	compressed := compressBrotli(original)
	if compressed == nil {
		t.Fatal("compressBrotli returned nil")
	}
	if len(compressed) >= len(original) {
		t.Fatalf("brotli did not shrink input: %d >= %d", len(compressed), len(original))
	}

	got, err := io.ReadAll(brotli.NewReader(bytes.NewReader(compressed)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatal("brotli round-trip changed the bytes")
	}
}

// ETags identify the selected representation, so a shared cache holding the
// gzip body must not be able to answer an identity request from it.
func TestETagForDistinguishesEncodings(t *testing.T) {
	identity := etagFor("abc123", "")
	gz := etagFor("abc123", "gzip")
	br := etagFor("abc123", "br")

	if identity == gz || identity == br || gz == br {
		t.Fatalf("etags collide: %q %q %q", identity, gz, br)
	}
	for _, tag := range []string{identity, gz, br} {
		if len(tag) < 2 || tag[0] != '"' || tag[len(tag)-1] != '"' {
			t.Errorf("etag %q is not a quoted strong validator", tag)
		}
	}
}
