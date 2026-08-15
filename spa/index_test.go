package spa

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestCacheControlFor(t *testing.T) {
	cfg := SPAConfig{Directory: "build"}
	cfg.applyDefaults()

	tests := []struct {
		key  string
		want string
	}{
		{"/_app/immutable/chunks/app.a1b2c3.js", "public, max-age=31536000, immutable"},
		{"/_app/immutable/assets/style.d4e5f6.css", "public, max-age=31536000, immutable"},
		{"/index.html", "no-cache"},
		{"/about.html", "no-cache"},
		{"/favicon.png", "public, max-age=3600, must-revalidate"},
		{"/robots.txt", "public, max-age=3600, must-revalidate"},
		{"/_app/version.json", "public, max-age=3600, must-revalidate"},
	}

	for _, tt := range tests {
		if got := cacheControlFor(tt.key, cfg); got != tt.want {
			t.Errorf("cacheControlFor(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

// An empty prefix list disables the immutable tier entirely.
func TestCacheControlForWithoutImmutableTier(t *testing.T) {
	cfg := SPAConfig{Directory: "build", ImmutablePrefixes: []string{}}
	cfg.applyDefaults()

	got := cacheControlFor("/_app/immutable/app.js", cfg)
	if got != "public, max-age=3600, must-revalidate" {
		t.Errorf("cacheControlFor = %q, want the asset tier", got)
	}
}

func mustBuild(t *testing.T, dir string, mutate ...func(*SPAConfig)) map[string]*entry {
	t.Helper()
	cfg := SPAConfig{Directory: dir}
	for _, m := range mutate {
		m(&cfg)
	}
	cfg.applyDefaults()
	index, _, err := buildIndex(dir, cfg)
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	return index
}

func keysOf(index map[string]*entry) []string {
	keys := make([]string, 0, len(index))
	for k := range index {
		keys = append(keys, k)
	}
	return keys
}

func TestBuildIndexKeysAreRootedURLPaths(t *testing.T) {
	index := mustBuild(t, buildDir(t))

	for _, key := range []string{"/index.html", "/_app/immutable/app.js", "/favicon.png"} {
		if _, ok := index[key]; !ok {
			t.Errorf("missing key %q; have %v", key, keysOf(index))
		}
	}
}

// A build directory can pick up .env from a careless copy step, and .git or
// .svelte-kit from the tooling. None of it is web content.
func TestBuildIndexSkipsDotfiles(t *testing.T) {
	dir := buildDir(t)
	writeFile(t, filepath.Join(dir, ".env"), "SECRET=hunter2")
	writeFile(t, filepath.Join(dir, ".git", "config"), "[core]")

	index := mustBuild(t, dir)

	if _, ok := index["/.env"]; ok {
		t.Error("indexed /.env")
	}
	if _, ok := index["/.git/config"]; ok {
		t.Error("indexed /.git/config")
	}
}

func TestBuildIndexServesDotfilesWhenEnabled(t *testing.T) {
	dir := buildDir(t)
	writeFile(t, filepath.Join(dir, ".well-known", "assetlinks.json"), "[]")

	index := mustBuild(t, dir, func(c *SPAConfig) { c.ServeDotfiles = true })

	if _, ok := index["/.well-known/assetlinks.json"]; !ok {
		t.Error("ServeDotfiles did not index /.well-known/assetlinks.json")
	}
}

// This is the escape the old implementation allowed: filepath.Join resolved
// ".." lexically but nothing stopped a symlink pointing out of the build.
func TestBuildIndexNeverFollowsSymlinks(t *testing.T) {
	secretDir := t.TempDir()
	writeFile(t, filepath.Join(secretDir, "passwd"), "root:x:0:0")

	dir := buildDir(t)
	if err := os.Symlink(secretDir, filepath.Join(dir, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join(secretDir, "passwd"), filepath.Join(dir, "leak.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	index := mustBuild(t, dir)

	for key := range index {
		if strings.HasPrefix(key, "/escape") || key == "/leak.txt" {
			t.Errorf("indexed through a symlink: %q", key)
		}
	}
}

func TestBuildIndexRespectsMaxFileSize(t *testing.T) {
	dir := buildDir(t)
	writeFile(t, filepath.Join(dir, "huge.txt"), strings.Repeat("x", 5000))

	index := mustBuild(t, dir, func(c *SPAConfig) { c.MaxFileSize = 1000 })

	if _, ok := index["/huge.txt"]; ok {
		t.Error("indexed a file above MaxFileSize")
	}
	if _, ok := index["/index.html"]; !ok {
		t.Error("MaxFileSize dropped a file that was under the limit")
	}
}

func TestBuildIndexCompressesOnlyWorthwhileFiles(t *testing.T) {
	index := mustBuild(t, buildDir(t))

	js := index["/_app/immutable/app.js"]
	if js.brotli == nil || js.gzip == nil {
		t.Error("compressible JS has no encoded variants")
	}
	if js.brotli != nil && len(js.brotli.data) >= len(js.identity.data) {
		t.Error("kept a brotli variant no smaller than the original")
	}

	// index.html is under MinCompressSize, and the fake PNG is both small
	// and an incompressible type.
	if index["/index.html"].compressed() {
		t.Error("compressed a file below MinCompressSize")
	}
	if index["/favicon.png"].compressed() {
		t.Error("compressed an image")
	}
}

// adapter-static's precompress option already produces these; reusing them
// saves the slowest part of boot.
func TestBuildIndexAdoptsPrecompressedSiblings(t *testing.T) {
	dir := buildDir(t)
	base := filepath.Join(dir, "_app", "immutable", "app.js")

	gz, err := compressGzip([]byte(jsSource))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base+".gz", gz, 0o644); err != nil {
		t.Fatal(err)
	}

	index := mustBuild(t, dir)

	if _, ok := index["/_app/immutable/app.js.gz"]; ok {
		t.Error("indexed a precompressed sibling as its own URL")
	}
	e := index["/_app/immutable/app.js"]
	if e.gzip == nil {
		t.Fatal("did not adopt the .gz sibling")
	}
	if !bytes.Equal(e.gzip.data, gz) {
		t.Error("adopted variant does not match the file on disk")
	}
}

// A sibling older than its base file is left over from a previous build and
// would serve stale bytes under a fresh ETag.
func TestBuildIndexIgnoresStalePrecompressedSiblings(t *testing.T) {
	dir := buildDir(t)
	base := filepath.Join(dir, "_app", "immutable", "app.js")

	if err := os.WriteFile(base+".br", []byte("stale garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(base+".br", old, old); err != nil {
		t.Fatal(err)
	}

	index := mustBuild(t, dir)

	e := index["/_app/immutable/app.js"]
	if e.brotli != nil && bytes.Equal(e.brotli.data, []byte("stale garbage")) {
		t.Error("adopted a stale precompressed sibling")
	}
}

func TestBuildIndexReportsStats(t *testing.T) {
	cfg := SPAConfig{Directory: buildDir(t)}
	cfg.applyDefaults()

	index, stats, err := buildIndex(cfg.Directory, cfg)
	if err != nil {
		t.Fatalf("buildIndex: %v", err)
	}
	if stats.raw <= 0 {
		t.Errorf("raw = %d, want > 0", stats.raw)
	}
	if stats.stored < stats.raw {
		t.Errorf("stored = %d, want >= raw = %d", stats.stored, stats.raw)
	}
	if len(index) != 3 {
		t.Errorf("indexed %d files, want 3", len(index))
	}
}
