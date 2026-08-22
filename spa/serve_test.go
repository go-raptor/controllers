package spa

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/go-raptor/raptor/v4/core"
)

// do runs one request through the controller, routing any returned error
// through the framework's error path so the recorder sees a real status.
func do(t *testing.T, sc *SPAController, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	ctx := core.NewContext(testCore(t), req, rec)
	if err := sc.Index(ctx); err != nil {
		ctx.Error(err)
	}
	return rec
}

// navRequest is a GET that looks like a browser navigating to a page.
func navRequest(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set(headerSecFetchDest, "document")
	return req
}

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

func TestIndexServesAnAsset(t *testing.T) {
	sc := newController(t, buildDir(t))

	rec := do(t, sc, httptest.NewRequest(http.MethodGet, "/_app/immutable/app.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if rec.Body.String() != jsSource {
		t.Error("body does not match the file")
	}
}

func TestIndexRejectsNonReadMethods(t *testing.T) {
	sc := newController(t, buildDir(t))

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rec := do(t, sc, httptest.NewRequest(method, "/_app/immutable/app.js", nil))

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: status %d, want 405", method, rec.Code)
		}
		if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
			t.Errorf("%s: Allow = %q, want \"GET, HEAD\"", method, allow)
		}
		if strings.Contains(rec.Body.String(), "console.log") {
			t.Errorf("%s: returned the file body", method)
		}
	}
}

func TestIndexServesHeadWithoutABody(t *testing.T) {
	sc := newController(t, buildDir(t))

	rec := do(t, sc, httptest.NewRequest(http.MethodHead, "/_app/immutable/app.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD returned %d body bytes", rec.Body.Len())
	}
}

func TestIndexFallsBackToTheShellForNavigations(t *testing.T) {
	sc := newController(t, buildDir(t))

	rec := do(t, sc, navRequest("/dashboard/settings"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "shell") {
		t.Error("did not serve the shell")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
}

// A route containing a dot is the case an extension heuristic would break.
func TestIndexFallsBackForRoutesContainingDots(t *testing.T) {
	sc := newController(t, buildDir(t))

	rec := do(t, sc, navRequest("/users/john.doe"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "shell") {
		t.Error("did not serve the shell")
	}
}

// A missing chunk must fail as a missing chunk, not arrive as HTML that the
// module loader then fails to parse.
func TestIndexReturns404ForMissingSubresources(t *testing.T) {
	sc := newController(t, buildDir(t))

	req := httptest.NewRequest(http.MethodGet, "/_app/immutable/gone.js", nil)
	req.Header.Set(headerSecFetchDest, "script")
	rec := do(t, sc, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "shell") {
		t.Error("served the HTML shell to a script request")
	}
}

func TestIndexTraversalAttemptsCannotEscape(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "secret.txt"), "top secret")
	dir := filepath.Join(base, "build")
	writeFile(t, filepath.Join(dir, "index.html"), "<!doctype html><title>shell</title>")
	sc := newController(t, dir)

	targets := []string{
		"/../secret.txt",
		"/../../secret.txt",
		"/_app/../../secret.txt",
		"/%2e%2e/secret.txt",
		"/..%2fsecret.txt",
	}

	for _, target := range targets {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set(headerSecFetchDest, "script")
		rec := do(t, sc, req)

		if strings.Contains(rec.Body.String(), "top secret") {
			t.Errorf("%s escaped the build directory", target)
		}
	}
}

func TestIndexServesBrotliWhenAccepted(t *testing.T) {
	sc := newController(t, buildDir(t))

	req := httptest.NewRequest(http.MethodGet, "/_app/immutable/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	rec := do(t, sc, req)

	if enc := rec.Header().Get("Content-Encoding"); enc != "br" {
		t.Fatalf("Content-Encoding = %q, want br", enc)
	}
	if vary := rec.Header().Get("Vary"); !strings.Contains(vary, "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to include Accept-Encoding", vary)
	}
	decoded, err := io.ReadAll(brotli.NewReader(bytes.NewReader(rec.Body.Bytes())))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(decoded) != jsSource {
		t.Error("brotli body does not decode to the original file")
	}
}

func TestIndexServesGzipWhenBrotliNotAccepted(t *testing.T) {
	sc := newController(t, buildDir(t))

	req := httptest.NewRequest(http.MethodGet, "/_app/immutable/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := do(t, sc, req)

	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	r, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	decoded, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(decoded) != jsSource {
		t.Error("gzip body does not decode to the original file")
	}
}

func TestIndexServesIdentityWithoutAcceptEncoding(t *testing.T) {
	sc := newController(t, buildDir(t))

	rec := do(t, sc, httptest.NewRequest(http.MethodGet, "/_app/immutable/app.js", nil))

	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q, want none", enc)
	}
	if rec.Body.String() != jsSource {
		t.Error("identity body does not match the file")
	}
}

// An incompressible file has no variants, so no shared cache needs to key
// on Accept-Encoding for it.
func TestIndexOmitsVaryForUncompressedFiles(t *testing.T) {
	sc := newController(t, buildDir(t))

	req := httptest.NewRequest(http.MethodGet, "/favicon.png", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	rec := do(t, sc, req)

	if vary := rec.Header().Get("Vary"); vary != "" {
		t.Errorf("Vary = %q, want empty", vary)
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding = %q, want none", enc)
	}
}

func TestIndexAnswersConditionalRequests(t *testing.T) {
	sc := newController(t, buildDir(t))

	first := do(t, sc, httptest.NewRequest(http.MethodGet, "/_app/immutable/app.js", nil))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on the first response")
	}

	req := httptest.NewRequest(http.MethodGet, "/_app/immutable/app.js", nil)
	req.Header.Set("If-None-Match", etag)
	second := do(t, sc, req)

	if second.Code != http.StatusNotModified {
		t.Fatalf("status %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Errorf("304 carried %d body bytes", second.Body.Len())
	}
}

// The gzip body is a different representation, so it must not validate
// against the identity ETag.
func TestIndexETagsDifferPerEncoding(t *testing.T) {
	sc := newController(t, buildDir(t))

	plain := do(t, sc, httptest.NewRequest(http.MethodGet, "/_app/immutable/app.js", nil))

	req := httptest.NewRequest(http.MethodGet, "/_app/immutable/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	encoded := do(t, sc, req)

	if plain.Header().Get("ETag") == encoded.Header().Get("ETag") {
		t.Error("identity and gzip responses share an ETag")
	}
}

func TestIndexSupportsRangeRequests(t *testing.T) {
	sc := newController(t, buildDir(t))

	req := httptest.NewRequest(http.MethodGet, "/_app/immutable/app.js", nil)
	req.Header.Set("Range", "bytes=0-9")
	rec := do(t, sc, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status %d, want 206", rec.Code)
	}
	if rec.Body.Len() != 10 {
		t.Errorf("got %d bytes, want 10", rec.Body.Len())
	}
	if rec.Body.String() != jsSource[:10] {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestIndexServesPrerenderedPages(t *testing.T) {
	dir := buildDir(t)
	writeFile(t, filepath.Join(dir, "about.html"), "<!doctype html>about page")
	sc := newController(t, dir)

	rec := do(t, sc, navRequest("/about"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "about page") {
		t.Error("served the shell instead of the prerendered page")
	}
}

// The design deliberately leaves these to middleware; this test records
// that so a later change is a decision rather than an accident.
func TestIndexSetsNoSecurityHeaders(t *testing.T) {
	sc := newController(t, buildDir(t))

	rec := do(t, sc, navRequest("/"))

	for _, header := range []string{
		"X-Content-Type-Options",
		"Content-Security-Policy",
		"X-Frame-Options",
		"Referrer-Policy",
	} {
		if v := rec.Header().Get(header); v != "" {
			t.Errorf("%s = %q, want empty (middleware owns this)", header, v)
		}
	}
}

// Nothing is indexed, so there is no shell to fall back to and no asset to
// find — including on the paths that reach the fallback rather than a map
// miss, which is where the nil dereference lived.
func TestIndexReturns404WithoutABuild(t *testing.T) {
	sc := newController(t, filepath.Join(t.TempDir(), "nope"), func(c *SPAConfig) { c.Optional = true })

	tests := []struct {
		name string
		req  *http.Request
	}{
		{"root", navRequest("/")},
		{"client-side route", navRequest("/dashboard/settings")},
		{"the index file itself", navRequest("/index.html")},
		{"subresource", httptest.NewRequest(http.MethodGet, "/_app/immutable/app.js", nil)},
		{"head", httptest.NewRequest(http.MethodHead, "/", nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, sc, tt.req)

			if rec.Code != http.StatusNotFound {
				t.Errorf("status %d, want 404", rec.Code)
			}
			if rec.Body.Len() != 0 {
				t.Errorf("404 carried a body: %q", rec.Body.String())
			}
		})
	}
}

// A 405 here would name GET in Allow, and that GET would 404 too, so the
// method check is skipped entirely when there is nothing to serve.
func TestIndexWithoutABuildDoesNotAdvertiseAllow(t *testing.T) {
	sc := newController(t, filepath.Join(t.TempDir(), "nope"), func(c *SPAConfig) { c.Optional = true })

	rec := do(t, sc, httptest.NewRequest(http.MethodPost, "/", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "" {
		t.Errorf("Allow = %q, want empty", allow)
	}
}
