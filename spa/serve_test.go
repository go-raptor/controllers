package spa

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestNegotiateEncoding(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		hasBrotli bool
		hasGzip   bool
		want      string
	}{
		{"empty header serves identity", "", true, true, ""},
		{"brotli preferred when both offered", "gzip, deflate, br", true, true, "br"},
		{"gzip when brotli not held", "gzip, deflate, br", false, true, "gzip"},
		{"identity when neither held", "gzip, br", false, false, ""},
		{"gzip when it is the only offer", "gzip", true, true, "gzip"},
		{"explicit q-values beat the default preference", "gzip;q=1.0, br;q=0.5", true, true, "gzip"},
		{"brotli wins an equal tie", "gzip;q=0.8, br;q=0.8", true, true, "br"},
		{"q=0 rejects an encoding", "br;q=0, gzip", true, true, "gzip"},
		{"all rejected serves identity", "br;q=0, gzip;q=0", true, true, ""},
		{"wildcard accepts anything", "*", true, true, "br"},
		{"wildcard with a rejection", "*;q=0, gzip", true, true, "gzip"},
		{"identity only", "identity", true, true, ""},
		{"whitespace and case are ignored", "  GZIP ; q=0.9 ,  BR ; q=1.0 ", true, true, "br"},
		{"unknown codings are ignored", "zstd, compress", true, true, ""},
		{"malformed q is treated as rejection", "br;q=notanumber, gzip", true, true, "gzip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := negotiateEncoding(tt.header, tt.hasBrotli, tt.hasGzip)
			if got != tt.want {
				t.Errorf("negotiateEncoding(%q, %v, %v) = %q, want %q",
					tt.header, tt.hasBrotli, tt.hasGzip, got, tt.want)
			}
		})
	}
}

func TestParseQuality(t *testing.T) {
	tests := []struct {
		params string
		want   float64
	}{
		{"", 1},
		{"q=0.5", 0.5},
		{" q=0.5 ", 0.5},
		{"Q=1.0", 1},
		{"charset=utf-8;q=0.25", 0.25},
		{"q=bogus", 0},
	}

	for _, tt := range tests {
		if got := parseQuality(tt.params); got != tt.want {
			t.Errorf("parseQuality(%q) = %v, want %v", tt.params, got, tt.want)
		}
	}
}

func TestNormalizeKey(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "/"},
		{"/", "/"},
		{"/index.html", "/index.html"},
		{"/about/", "/about"},
		{"/a//b///c", "/a/b/c"},
		{"/./about", "/about"},
		{"/../../etc/passwd", "/etc/passwd"},
		{"/app/../../../etc/passwd", "/etc/passwd"},
		{"noslash", "/noslash"},
	}

	for _, tt := range tests {
		if got := normalizeKey(tt.in); got != tt.want {
			t.Errorf("normalizeKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// adapter-static writes prerendered pages either as about.html or as
// about/index.html depending on trailingSlash, and both must resolve.
func TestLookupResolvesPrerenderedLayouts(t *testing.T) {
	dir := buildDir(t)
	writeFile(t, filepath.Join(dir, "about.html"), "<!doctype html>about")
	writeFile(t, filepath.Join(dir, "docs", "index.html"), "<!doctype html>docs")
	sc := newController(t, dir)

	tests := []struct {
		key     string
		wantKey string
	}{
		{"/index.html", "/index.html"},
		{"/about", "/about.html"},
		{"/docs", "/docs/index.html"},
		{"/_app/immutable/app.js", "/_app/immutable/app.js"},
	}

	for _, tt := range tests {
		got, ok := sc.lookup(tt.key)
		if !ok {
			t.Errorf("lookup(%q) missed", tt.key)
			continue
		}
		if got != sc.index[tt.wantKey] {
			t.Errorf("lookup(%q) did not resolve to %q", tt.key, tt.wantKey)
		}
	}
}

func TestLookupRootServesTheShell(t *testing.T) {
	sc := newController(t, buildDir(t))

	got, ok := sc.lookup("/")
	if !ok || got != sc.fallback {
		t.Error("lookup(\"/\") did not resolve to the shell")
	}
}

func TestLookupMissesUnknownPaths(t *testing.T) {
	sc := newController(t, buildDir(t))

	for _, key := range []string{"/nope", "/etc/passwd", "/_app/immutable/gone.js"} {
		if _, ok := sc.lookup(key); ok {
			t.Errorf("lookup(%q) unexpectedly hit", key)
		}
	}
}

func TestIsNavigation(t *testing.T) {
	tests := []struct {
		name   string
		dest   string
		accept string
		want   bool
	}{
		{"browser navigation", "document", "text/html,*/*", true},
		{"iframe", "iframe", "text/html", true},
		{"script subresource", "script", "*/*", false},
		{"stylesheet subresource", "style", "*/*", false},
		{"image subresource", "image", "*/*", false},
		{"fetch or xhr", "empty", "*/*", false},
		{"legacy client asking for html", "", "text/html,application/xhtml+xml", true},
		{"curl default", "", "*/*", false},
		{"no headers at all", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/route", nil)
			if tt.dest != "" {
				req.Header.Set(headerSecFetchDest, tt.dest)
			}
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			if got := isNavigation(req); got != tt.want {
				t.Errorf("isNavigation() = %v, want %v", got, tt.want)
			}
		})
	}
}
