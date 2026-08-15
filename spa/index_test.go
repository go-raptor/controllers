package spa

import "testing"

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
