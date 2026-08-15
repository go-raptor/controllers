# SPA Controller Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild `controllers/spa` so the entire frontend build is read, compressed, and hashed once at startup, making every request a map lookup with correct caching, encoding, and conditional-request behaviour.

**Architecture:** `Setup()` walks the build directory once, holding each file's identity/gzip/brotli bytes, ETag, Content-Type and Cache-Control in memory. Request handling never touches the filesystem — a URL path becomes a map key, so path traversal has nothing to traverse. Compression happens in parallel at boot, so per-request CPU is zero.

**Tech Stack:** Go 1.26, `github.com/go-raptor/raptor/v4` v4.3.1, `github.com/andybalholm/brotli` v1.2.2, stdlib `compress/gzip`, `crypto/sha256`, `net/http.ServeContent`.

**Spec:** None. The design was approved inline in the originating session and is restated in "Design Summary" below; this plan is the authoritative document.

## Design Summary

Four decisions were made by the repo owner and are not open for reinterpretation during implementation:

1. **Serving model** — index *and file contents* held in RAM. Restart is required to pick up a rebuilt frontend. This is intended.
2. **Compression** — brotli + gzip produced at startup, never per request. New dependency on `andybalholm/brotli` is approved.
3. **Security headers** — the controller sets **none**. No `X-Content-Type-Options`, no CSP, no `X-Frame-Options`, no `Referrer-Policy`. These belong to middleware that does not exist yet. Do not add them. The controller sets caching and encoding headers only.
4. **Fallback** — the SPA shell is served only for page navigations (`Sec-Fetch-Dest`, else `Accept: text/html`). Everything else 404s.

## Global Constraints

- Go directive stays `go 1.26`. Do not lower it.
- `github.com/go-raptor/raptor/v4` must be `v4.3.1` (currently `v4.1.7`).
- `github.com/andybalholm/brotli` must be `v1.2.2`. No other new direct dependencies.
- Package name stays `spa`. Import path stays `github.com/go-raptor/controllers/spa`.
- The controller sets no security headers (see Design Summary #3).
- Compression level is always maximum (`brotli.BestCompression`, `gzip.BestCompression`) — it is a one-time boot cost. Do not add a config field for it.
- Every comment must explain *why*, matching the style in `middlewares/cors/cors.go` and `raptor/v4/core/file.go`. No comments restating what the code says.
- Tests use the stdlib `testing` package only, table-driven where there is more than one case, matching `raptor/v4/core/file_test.go`.
- Commit after every task with a `feat:`/`test:`/`refactor:` prefix.

## File Structure

| File | Responsibility |
|---|---|
| `spa_controller.go` (rewrite) | `SPAConfig`, `DefaultSPAConfig`, `SPAController`, `Setup()` |
| `index.go` (create) | Walking the build dir, reading, compressing, ETagging — everything that happens at boot |
| `serve.go` (create) | Request handling: method gate, key lookup, encoding negotiation, response writing |
| `spa_test.go` (create) | Shared test helpers |
| `index_test.go` (create) | Boot-time behaviour: walk rules, compression, cache tiers |
| `serve_test.go` (create) | Request-time behaviour: negotiation, fallback, conditional requests |
| `bench_test.go` (create) | Serving benchmark |
| `go.mod` / `go.sum` (modify) | Dependency bumps |

---

### Task 1: Dependencies and configuration

**Files:**
- Modify: `go.mod`
- Rewrite: `spa_controller.go`
- Create: `spa_test.go`
- Test: `spa_controller_config_test.go`

**Interfaces:**
- Consumes: nothing (first task)
- Produces: `SPAConfig` struct (fields listed below), `DefaultSPAConfig` var, `(*SPAConfig).applyDefaults()`, `SPAController` struct with fields `config SPAConfig`, `index map[string]*entry`, `fallback *entry`, and `NewSPAController(config SPAConfig) *SPAController`. Later tasks rely on exactly these names.

- [ ] **Step 1: Update dependencies**

```bash
cd /home/h00s/dev/go/go-raptor/controllers/spa
go get github.com/go-raptor/raptor/v4@v4.3.1
go get github.com/andybalholm/brotli@v1.2.2
go mod tidy
```

Expected: `go.mod` requires `raptor/v4 v4.3.1` and `brotli v1.2.2` as direct dependencies.

- [ ] **Step 2: Write the failing config test**

Create `spa_controller_config_test.go`:

```go
package spa

import "testing"

func TestApplyDefaultsFillsEmptyFields(t *testing.T) {
	cfg := SPAConfig{Directory: "build"}
	cfg.applyDefaults()

	if cfg.IndexFile != "index.html" {
		t.Errorf("IndexFile = %q, want index.html", cfg.IndexFile)
	}
	if cfg.DocumentCacheControl != "no-cache" {
		t.Errorf("DocumentCacheControl = %q, want no-cache", cfg.DocumentCacheControl)
	}
	if cfg.ImmutableCacheControl != "public, max-age=31536000, immutable" {
		t.Errorf("ImmutableCacheControl = %q", cfg.ImmutableCacheControl)
	}
	if cfg.AssetCacheControl != "public, max-age=3600, must-revalidate" {
		t.Errorf("AssetCacheControl = %q", cfg.AssetCacheControl)
	}
	if cfg.MinCompressSize != 1024 {
		t.Errorf("MinCompressSize = %d, want 1024", cfg.MinCompressSize)
	}
	if len(cfg.ImmutablePrefixes) != 1 || cfg.ImmutablePrefixes[0] != "/_app/immutable/" {
		t.Errorf("ImmutablePrefixes = %v", cfg.ImmutablePrefixes)
	}
	if cfg.Directory != "build" {
		t.Errorf("applyDefaults overwrote Directory: %q", cfg.Directory)
	}
}

func TestApplyDefaultsPreservesExplicitValues(t *testing.T) {
	cfg := SPAConfig{
		Directory:            "dist",
		IndexFile:            "app.html",
		DocumentCacheControl: "no-store",
		MinCompressSize:      2048,
	}
	cfg.applyDefaults()

	if cfg.IndexFile != "app.html" {
		t.Errorf("IndexFile = %q, want app.html", cfg.IndexFile)
	}
	if cfg.DocumentCacheControl != "no-store" {
		t.Errorf("DocumentCacheControl = %q, want no-store", cfg.DocumentCacheControl)
	}
	if cfg.MinCompressSize != 2048 {
		t.Errorf("MinCompressSize = %d, want 2048", cfg.MinCompressSize)
	}
}

// An explicitly empty slice means "no immutable tier" and must survive,
// which a len()==0 check would quietly overwrite.
func TestApplyDefaultsKeepsEmptyImmutablePrefixes(t *testing.T) {
	cfg := SPAConfig{Directory: "build", ImmutablePrefixes: []string{}}
	cfg.applyDefaults()

	if len(cfg.ImmutablePrefixes) != 0 {
		t.Errorf("ImmutablePrefixes = %v, want empty", cfg.ImmutablePrefixes)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./... -run TestApplyDefaults -v`
Expected: FAIL — `undefined: SPAConfig`

- [ ] **Step 4: Rewrite `spa_controller.go`**

Replace the file's entire contents:

```go
// Package spa serves a single-page application from a directory of built
// static files. The whole build is read into memory at startup, so serving
// a request is a map lookup: nothing derived from the request ever reaches
// the filesystem.
package spa

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-raptor/raptor/v4"
)

// SPAConfig configures the controller. Directory is required; every other
// field falls back to DefaultSPAConfig.
type SPAConfig struct {
	// Directory is the built frontend to serve, e.g. "build".
	Directory string `yaml:"directory"`

	// IndexFile is the shell served for client-side routes.
	IndexFile string `yaml:"index_file"`

	// ImmutablePrefixes are URL prefixes holding content-hashed assets. A
	// nil slice takes the default; an empty one disables the tier.
	ImmutablePrefixes []string `yaml:"immutable_prefixes"`

	// ImmutableCacheControl applies under ImmutablePrefixes.
	ImmutableCacheControl string `yaml:"immutable_cache_control"`

	// DocumentCacheControl applies to .html files.
	DocumentCacheControl string `yaml:"document_cache_control"`

	// AssetCacheControl applies to everything else.
	AssetCacheControl string `yaml:"asset_cache_control"`

	// ServeDotfiles indexes dot-prefixed files and directories. Off by
	// default so a stray .env or .git in the build output cannot be served.
	ServeDotfiles bool `yaml:"serve_dotfiles"`

	// MaxFileSize skips files above this size in bytes; 0 means no limit.
	// Every indexed file is held for the process lifetime, so a stray video
	// in the build directory is a memory leak with extra steps.
	MaxFileSize int64 `yaml:"max_file_size"`

	// MinCompressSize is the smallest file worth compressing. Below roughly
	// one MTU the encoding overhead outweighs the saving.
	MinCompressSize int64 `yaml:"min_compress_size"`
}

var DefaultSPAConfig = SPAConfig{
	IndexFile:             "index.html",
	ImmutablePrefixes:     []string{"/_app/immutable/"},
	ImmutableCacheControl: "public, max-age=31536000, immutable",
	DocumentCacheControl:  "no-cache",
	AssetCacheControl:     "public, max-age=3600, must-revalidate",
	MinCompressSize:       1024,
}

func (c *SPAConfig) applyDefaults() {
	if c.IndexFile == "" {
		c.IndexFile = DefaultSPAConfig.IndexFile
	}
	if c.ImmutablePrefixes == nil {
		c.ImmutablePrefixes = DefaultSPAConfig.ImmutablePrefixes
	}
	if c.ImmutableCacheControl == "" {
		c.ImmutableCacheControl = DefaultSPAConfig.ImmutableCacheControl
	}
	if c.DocumentCacheControl == "" {
		c.DocumentCacheControl = DefaultSPAConfig.DocumentCacheControl
	}
	if c.AssetCacheControl == "" {
		c.AssetCacheControl = DefaultSPAConfig.AssetCacheControl
	}
	if c.MinCompressSize == 0 {
		c.MinCompressSize = DefaultSPAConfig.MinCompressSize
	}
}

type SPAController struct {
	raptor.Controller

	config   SPAConfig
	index    map[string]*entry
	fallback *entry
}

func NewSPAController(config SPAConfig) *SPAController {
	return &SPAController{config: config}
}

// Setup reads the entire build directory into memory. Raptor calls it after
// resources are injected, and a returned error aborts boot — a missing or
// unbuilt frontend should fail at startup, not on the first request.
func (sc *SPAController) Setup() error {
	sc.config.applyDefaults()

	if sc.config.Directory == "" {
		return fmt.Errorf("spa: Directory is required")
	}
	root, err := filepath.Abs(sc.config.Directory)
	if err != nil {
		return fmt.Errorf("spa: resolving directory %q: %w", sc.config.Directory, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("spa: reading directory %q: %w", root, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("spa: %q is not a directory", root)
	}

	index, stats, err := buildIndex(root, sc.config)
	if err != nil {
		return fmt.Errorf("spa: indexing %q: %w", root, err)
	}

	indexKey := "/" + strings.TrimPrefix(filepath.ToSlash(sc.config.IndexFile), "/")
	fallback, ok := index[indexKey]
	if !ok {
		return fmt.Errorf("spa: index file %q not found in %q", sc.config.IndexFile, root)
	}

	sc.index = index
	sc.fallback = fallback

	sc.Log.Info("SPA index built",
		"directory", root,
		"files", len(index),
		"raw_bytes", stats.raw,
		"stored_bytes", stats.stored,
		"skipped", stats.skipped,
	)
	return nil
}
```

- [ ] **Step 5: Create the shared test helpers**

Create `spa_test.go`:

```go
package spa

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-raptor/raptor/v4/config"
	"github.com/go-raptor/raptor/v4/core"
)

// testCore builds the minimum Core a Context needs. NewCore dereferences
// Resources.Config, so a config must be set before calling it.
func testCore(t *testing.T) *core.Core {
	t.Helper()
	resources := core.NewResources()
	resources.SetLogHandler(slog.NewTextHandler(io.Discard, nil))
	resources.SetConfig(config.NewConfigDefaults())
	return core.NewCore(resources)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// jsSource is large enough to clear MinCompressSize and repetitive enough
// to compress well, so encoding assertions are meaningful.
var jsSource = strings.Repeat("console.log('hello world');\n", 100)

// buildDir creates a temp directory shaped like an adapter-static build.
func buildDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "index.html"), "<!doctype html><title>shell</title>")
	writeFile(t, filepath.Join(dir, "_app", "immutable", "app.js"), jsSource)
	writeFile(t, filepath.Join(dir, "favicon.png"), "\x89PNG\r\n\x1a\nnot really a png")
	return dir
}

// newController builds and starts a controller over dir. Each mutate func
// adjusts the config before Setup runs.
func newController(t *testing.T, dir string, mutate ...func(*SPAConfig)) *SPAController {
	t.Helper()
	cfg := SPAConfig{Directory: dir}
	for _, m := range mutate {
		m(&cfg)
	}
	sc := NewSPAController(cfg)
	sc.Init(testCore(t).Resources)
	if err := sc.Setup(); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return sc
}

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
	req.Header.Set("Sec-Fetch-Dest", "document")
	return req
}
```

- [ ] **Step 6: Run the config tests**

Run: `go test ./... -run TestApplyDefaults -v`
Expected: PASS (3 tests). The package will not compile until Task 4 provides `entry` and `buildIndex` — if the build fails on those two identifiers only, that is expected; proceed to Task 2 and re-run at Task 6.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum spa_controller.go spa_test.go spa_controller_config_test.go
git commit -m "feat(spa): add SPAConfig with defaults and bump dependencies"
```

---

### Task 2: Content type and compressibility

**Files:**
- Create: `index.go`
- Test: `index_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks
- Produces: `contentTypeFor(path string, data []byte) string`, `isCompressible(contentType string) bool`

- [ ] **Step 1: Write the failing tests**

Create `index_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run 'TestContentTypeFor|TestIsCompressible' -v`
Expected: FAIL — `undefined: contentTypeFor`, `undefined: isCompressible`

- [ ] **Step 3: Create `index.go` with the two functions**

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -run 'TestContentTypeFor|TestIsCompressible' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add index.go index_test.go
git commit -m "feat(spa): resolve content types deterministically and classify compressibility"
```

---

### Task 3: Representations, ETags and compression

**Files:**
- Modify: `index.go`
- Test: `index_test.go`

**Interfaces:**
- Consumes: `isCompressible` (Task 2)
- Produces: `representation{data []byte, etag string}`, `entry{identity representation, gzip *representation, brotli *representation, contentType, cacheControl string, modTime time.Time}`, `(*entry).compressed() bool`, `(*entry).storedBytes() int64`, `compressGzip([]byte) ([]byte, error)`, `compressBrotli([]byte) []byte`, `etagFor(base, encoding string) string`

- [ ] **Step 1: Write the failing tests**

Append to `index_test.go`:

```go
import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	"github.com/andybalholm/brotli"
)

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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run 'TestCompress|TestETagFor' -v`
Expected: FAIL — `undefined: compressGzip`, `undefined: compressBrotli`, `undefined: etagFor`

- [ ] **Step 3: Add the types and functions to `index.go`**

Add these imports to `index.go`: `bytes`, `compress/gzip`, `time`, `github.com/andybalholm/brotli`.

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -run 'TestCompress|TestETagFor' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add index.go index_test.go
git commit -m "feat(spa): add in-memory representations with per-encoding ETags"
```

---

### Task 4: Cache-Control tiers

**Files:**
- Modify: `index.go`
- Test: `index_test.go`

**Interfaces:**
- Consumes: `SPAConfig` (Task 1)
- Produces: `cacheControlFor(key string, cfg SPAConfig) string`

- [ ] **Step 1: Write the failing test**

Append to `index_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run TestCacheControlFor -v`
Expected: FAIL — `undefined: cacheControlFor`

- [ ] **Step 3: Add `cacheControlFor` to `index.go`**

```go
// cacheControlFor picks a caching tier. Content-hashed assets can be kept
// forever because a new build gives them a new URL; documents must be
// revalidated or a returning visitor would never see a deploy.
func cacheControlFor(key string, cfg SPAConfig) string {
	for _, prefix := range cfg.ImmutablePrefixes {
		if strings.HasPrefix(key, prefix) {
			return cfg.ImmutableCacheControl
		}
	}
	if strings.HasSuffix(key, ".html") {
		return cfg.DocumentCacheControl
	}
	return cfg.AssetCacheControl
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run TestCacheControlFor -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add index.go index_test.go
git commit -m "feat(spa): assign cache-control by asset tier"
```

---

### Task 5: Building the index

**Files:**
- Modify: `index.go`
- Test: `index_test.go`

**Interfaces:**
- Consumes: `contentTypeFor`, `isCompressible` (Task 2); `entry`, `representation`, `etagFor`, `compressGzip`, `compressBrotli` (Task 3); `cacheControlFor` (Task 4); `SPAConfig` (Task 1)
- Produces: `indexStats{raw, stored int64, skipped int}`, `buildIndex(root string, cfg SPAConfig) (map[string]*entry, indexStats, error)`

- [ ] **Step 1: Write the failing tests**

Append to `index_test.go` (add imports `os`, `path/filepath`, `runtime`, `strings`):

```go
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

func TestBuildIndexKeysAreRootedURLPaths(t *testing.T) {
	index := mustBuild(t, buildDir(t))

	for _, key := range []string{"/index.html", "/_app/immutable/app.js", "/favicon.png"} {
		if _, ok := index[key]; !ok {
			t.Errorf("missing key %q; have %v", key, keysOf(index))
		}
	}
}

func keysOf(index map[string]*entry) []string {
	keys := make([]string, 0, len(index))
	for k := range index {
		keys = append(keys, k)
	}
	return keys
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
	entry := index["/_app/immutable/app.js"]
	if entry.gzip == nil {
		t.Fatal("did not adopt the .gz sibling")
	}
	if !bytes.Equal(entry.gzip.data, gz) {
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

	entry := index["/_app/immutable/app.js"]
	if entry.brotli != nil && bytes.Equal(entry.brotli.data, []byte("stale garbage")) {
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
```

Add `bytes` and `time` to the test imports if not already present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run TestBuildIndex -v`
Expected: FAIL — `undefined: buildIndex`

- [ ] **Step 3: Add the index builder to `index.go`**

Add imports: `io/fs`, `os`, `runtime`, `sync`, `crypto/sha256`, `encoding/hex`.

```go
type indexStats struct {
	raw     int64
	stored  int64
	skipped int
}

// candidate is a file the walk accepted, waiting for the expensive read and
// compress step.
type candidate struct {
	path    string
	key     string
	modTime time.Time
}

func buildIndex(root string, cfg SPAConfig) (map[string]*entry, indexStats, error) {
	candidates, stats, err := collectCandidates(root, cfg)
	if err != nil {
		return nil, stats, err
	}

	entries := make([]*entry, len(candidates))
	failures := make([]error, len(candidates))

	// Brotli at maximum level over a whole build is slow enough to show up
	// in boot time, and each file is independent, so fan out across cores.
	workers := min(runtime.GOMAXPROCS(0), len(candidates))
	work := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				entries[i], failures[i] = buildEntry(candidates[i], cfg)
			}
		}()
	}
	for i := range candidates {
		work <- i
	}
	close(work)
	wg.Wait()

	index := make(map[string]*entry, len(candidates))
	for i, e := range entries {
		if failures[i] != nil {
			return nil, stats, failures[i]
		}
		index[candidates[i].key] = e
		stats.raw += int64(len(e.identity.data))
		stats.stored += e.storedBytes()
	}
	return index, stats, nil
}

func collectCandidates(root string, cfg SPAConfig) ([]candidate, indexStats, error) {
	var candidates []candidate
	var stats indexStats

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// The root's own name is not content, and testing it for a leading
		// dot would prune the whole walk for a build dir like ".output".
		if path == root {
			return nil
		}

		if !cfg.ServeDotfiles && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			stats.skipped++
			return nil
		}

		// WalkDir reports symlinks without following them, and we keep it
		// that way: a link inside the build directory can point anywhere on
		// the filesystem, and following one is exactly the escape this
		// design exists to prevent.
		if d.Type()&fs.ModeSymlink != 0 {
			stats.skipped++
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			stats.skipped++
			return nil
		}
		// Precompressed siblings are folded into their base file's entry by
		// buildEntry, so they must not become URLs of their own.
		if strings.HasSuffix(d.Name(), ".br") || strings.HasSuffix(d.Name(), ".gz") {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if cfg.MaxFileSize > 0 && info.Size() > cfg.MaxFileSize {
			stats.skipped++
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		candidates = append(candidates, candidate{
			path:    path,
			key:     "/" + filepath.ToSlash(rel),
			modTime: info.ModTime(),
		})
		return nil
	})
	return candidates, stats, err
}

func buildEntry(c candidate, cfg SPAConfig) (*entry, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, err
	}

	contentType := contentTypeFor(c.path, data)
	sum := sha256.Sum256(data)
	base := hex.EncodeToString(sum[:16])

	e := &entry{
		identity:     representation{data: data, etag: etagFor(base, "")},
		contentType:  contentType,
		cacheControl: cacheControlFor(c.key, cfg),
		modTime:      c.modTime,
	}

	if int64(len(data)) < cfg.MinCompressSize || !isCompressible(contentType) {
		return e, nil
	}

	br := precompressedSibling(c, ".br")
	if br == nil {
		br = compressBrotli(data)
	}
	if br != nil && len(br) < len(data) {
		e.brotli = &representation{data: br, etag: etagFor(base, "br")}
	}

	gz := precompressedSibling(c, ".gz")
	if gz == nil {
		if gz, err = compressGzip(data); err != nil {
			return nil, err
		}
	}
	if gz != nil && len(gz) < len(data) {
		e.gzip = &representation{data: gz, etag: etagFor(base, "gzip")}
	}

	return e, nil
}

// precompressedSibling returns the contents of path+ext when the build
// already produced it, saving the slowest part of startup. It refuses
// symlinks for the same reason the walk does, and anything older than the
// file it claims to encode, which would be left over from a previous build.
func precompressedSibling(c candidate, ext string) []byte {
	sibling := c.path + ext
	info, err := os.Lstat(sibling)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	if info.ModTime().Before(c.modTime) {
		return nil
	}
	data, err := os.ReadFile(sibling)
	if err != nil {
		return nil
	}
	return data
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -run TestBuildIndex -v`
Expected: PASS (9 tests)

- [ ] **Step 5: Commit**

```bash
git add index.go index_test.go
git commit -m "feat(spa): build the in-memory index at startup without following symlinks"
```

---

### Task 6: Setup wiring and failure modes

**Files:**
- Modify: `spa_controller.go` (only if Step 3 finds a defect)
- Test: `spa_controller_config_test.go`

**Interfaces:**
- Consumes: `buildIndex`, `indexStats` (Task 5); `SPAController`, `Setup` (Task 1)
- Produces: nothing new — this task proves Task 1's `Setup` against a real index

- [ ] **Step 1: Write the failing tests**

Append to `spa_controller_config_test.go` (imports: `path/filepath`, `strings`, `testing`):

```go
func TestSetupIndexesTheBuild(t *testing.T) {
	sc := newController(t, buildDir(t))

	if len(sc.index) != 3 {
		t.Errorf("indexed %d files, want 3", len(sc.index))
	}
	if sc.fallback == nil {
		t.Fatal("fallback entry is nil")
	}
	if sc.fallback != sc.index["/index.html"] {
		t.Error("fallback is not the index file's entry")
	}
}

func TestSetupFailsWithoutDirectory(t *testing.T) {
	sc := NewSPAController(SPAConfig{})
	sc.Init(testCore(t).Resources)

	err := sc.Setup()
	if err == nil {
		t.Fatal("Setup succeeded with no Directory")
	}
	if !strings.Contains(err.Error(), "Directory is required") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestSetupFailsOnMissingDirectory(t *testing.T) {
	sc := NewSPAController(SPAConfig{Directory: filepath.Join(t.TempDir(), "nope")})
	sc.Init(testCore(t).Resources)

	if err := sc.Setup(); err == nil {
		t.Fatal("Setup succeeded with a missing directory")
	}
}

// An unbuilt frontend is the common deploy mistake; it must stop boot rather
// than 404 every page at runtime.
func TestSetupFailsWhenIndexFileMissing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "app.js"), jsSource)

	sc := NewSPAController(SPAConfig{Directory: dir})
	sc.Init(testCore(t).Resources)

	err := sc.Setup()
	if err == nil {
		t.Fatal("Setup succeeded without an index file")
	}
	if !strings.Contains(err.Error(), "index.html") {
		t.Errorf("error does not name the missing file: %v", err)
	}
}

func TestSetupFailsWhenDirectoryIsAFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notadir")
	writeFile(t, file, "x")

	sc := NewSPAController(SPAConfig{Directory: file})
	sc.Init(testCore(t).Resources)

	if err := sc.Setup(); err == nil {
		t.Fatal("Setup succeeded on a regular file")
	}
}

func TestSetupHonoursCustomIndexFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "shell.html"), "<!doctype html>")

	sc := newController(t, dir, func(c *SPAConfig) { c.IndexFile = "shell.html" })

	if sc.fallback != sc.index["/shell.html"] {
		t.Error("custom IndexFile was not used as the fallback")
	}
}
```

- [ ] **Step 2: Run the whole suite**

Run: `go test ./... -v`
Expected: PASS for everything written so far. The package should now compile fully, since `entry` and `buildIndex` exist.

- [ ] **Step 3: Fix any defect the tests expose**

If a test fails, correct `spa_controller.go` — do not weaken the test. The likely culprits are the `indexKey` construction (`"/" + strings.TrimPrefix(filepath.ToSlash(sc.config.IndexFile), "/")`) and defaults not being applied before `buildIndex` runs.

- [ ] **Step 4: Confirm green**

Run: `go test ./... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add spa_controller.go spa_controller_config_test.go
git commit -m "test(spa): cover Setup success and startup failure modes"
```

---

### Task 7: Accept-Encoding negotiation

**Files:**
- Create: `serve.go`
- Test: `serve_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks
- Produces: constants `encodingBrotli = "br"`, `encodingGzip = "gzip"`, `headerETag = "ETag"`, `headerSecFetchDest = "Sec-Fetch-Dest"`; `negotiateEncoding(header string, hasBrotli, hasGzip bool) string`; `parseQuality(params string) float64`

- [ ] **Step 1: Write the failing tests**

Create `serve_test.go`:

```go
package spa

import "testing"

func TestNegotiateEncoding(t *testing.T) {
	tests := []struct {
		name       string
		header     string
		hasBrotli  bool
		hasGzip    bool
		want       string
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run 'TestNegotiateEncoding|TestParseQuality' -v`
Expected: FAIL — `undefined: negotiateEncoding`, `undefined: parseQuality`

- [ ] **Step 3: Create `serve.go` with the negotiator**

```go
package spa

import (
	"strconv"
	"strings"
)

const (
	encodingBrotli     = "br"
	encodingGzip       = "gzip"
	headerETag         = "ETag"
	headerSecFetchDest = "Sec-Fetch-Dest"
)

// negotiateEncoding picks the best encoding the client accepts that we
// actually hold. Brotli wins ties because it compresses better, but an
// explicit q-value overrides that: "gzip;q=1.0, br;q=0.5" asks for gzip.
// Returns "" for identity.
func negotiateEncoding(header string, hasBrotli, hasGzip bool) string {
	if header == "" || (!hasBrotli && !hasGzip) {
		return ""
	}

	const unset = -1.0
	star, br, gz := unset, unset, unset

	for len(header) > 0 {
		var field string
		if i := strings.IndexByte(header, ','); i >= 0 {
			field, header = header[:i], header[i+1:]
		} else {
			field, header = header, ""
		}
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}

		name, quality := field, 1.0
		if i := strings.IndexByte(field, ';'); i >= 0 {
			name = strings.TrimSpace(field[:i])
			quality = parseQuality(field[i+1:])
		}

		switch strings.ToLower(name) {
		case encodingBrotli:
			br = quality
		case encodingGzip:
			gz = quality
		case "*":
			star = quality
		}
	}

	// A coding the header never names inherits the wildcard's quality.
	if br == unset {
		br = star
	}
	if gz == unset {
		gz = star
	}

	if hasBrotli && br > 0 && br >= gz {
		return encodingBrotli
	}
	if hasGzip && gz > 0 {
		return encodingGzip
	}
	return ""
}

// parseQuality reads the q parameter from one Accept-Encoding field,
// defaulting to 1 when absent and 0 when malformed — a value we cannot
// parse is not a preference we should guess at.
func parseQuality(params string) float64 {
	for len(params) > 0 {
		var param string
		if i := strings.IndexByte(params, ';'); i >= 0 {
			param, params = params[:i], params[i+1:]
		} else {
			param, params = params, ""
		}
		param = strings.TrimSpace(param)
		if len(param) < 2 || !strings.EqualFold(param[:2], "q=") {
			continue
		}
		quality, err := strconv.ParseFloat(strings.TrimSpace(param[2:]), 64)
		if err != nil {
			return 0
		}
		return quality
	}
	return 1
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -run 'TestNegotiateEncoding|TestParseQuality' -v`
Expected: PASS (15 subtests + 6 quality cases)

- [ ] **Step 5: Commit**

```bash
git add serve.go serve_test.go
git commit -m "feat(spa): negotiate Accept-Encoding with q-value support"
```

---

### Task 8: Key normalization, lookup and navigation detection

**Files:**
- Modify: `serve.go`
- Test: `serve_test.go`

**Interfaces:**
- Consumes: `SPAController` (Task 1), `entry` (Task 3), constants (Task 7)
- Produces: `normalizeKey(p string) string`, `(*SPAController).lookup(key string) (*entry, bool)`, `isNavigation(r *http.Request) bool`

- [ ] **Step 1: Write the failing tests**

Append to `serve_test.go` (add imports `net/http`, `net/http/httptest`, `path/filepath`):

```go
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
		key      string
		wantKey  string
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run 'TestNormalizeKey|TestLookup|TestIsNavigation' -v`
Expected: FAIL — `undefined: normalizeKey`, `undefined: isNavigation`, and `sc.lookup` undefined

- [ ] **Step 3: Add the three functions to `serve.go`**

Add imports: `net/http`, `path`.

```go
// normalizeKey turns a request path into an index key. net/http has already
// percent-decoded URL.Path, and path.Clean collapses "." and ".." segments —
// but only so lookups behave predictably. It is not a containment check: an
// escaping key simply fails to match anything, because keys are the only
// thing a request can address.
func normalizeKey(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if cleaned := path.Clean(p); cleaned != "." {
		return cleaned
	}
	return "/"
}

// lookup resolves a URL path to an indexed file, accounting for the two
// layouts adapter-static produces for prerendered pages: "/about" is stored
// as about.html under trailingSlash "never" and about/index.html under
// "always".
func (sc *SPAController) lookup(key string) (*entry, bool) {
	if e, ok := sc.index[key]; ok {
		return e, true
	}
	if key == "/" {
		return sc.fallback, true
	}
	if e, ok := sc.index[key+".html"]; ok {
		return e, true
	}
	if e, ok := sc.index[key+"/index.html"]; ok {
		return e, true
	}
	return nil, false
}

// isNavigation reports whether a request is a browser going to a page rather
// than fetching a subresource. Sec-Fetch-Dest is the reliable signal and
// every current browser sends it; the Accept check covers older clients.
// A plain "curl /route" sends neither and gets a 404 — pass
// -H 'Accept: text/html' to see what a browser would.
func isNavigation(r *http.Request) bool {
	switch r.Header.Get(headerSecFetchDest) {
	case "document", "iframe", "frame", "embed", "object":
		return true
	case "":
		return strings.Contains(r.Header.Get("Accept"), "text/html")
	default:
		return false
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... -run 'TestNormalizeKey|TestLookup|TestIsNavigation' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add serve.go serve_test.go
git commit -m "feat(spa): resolve prerendered layouts and detect page navigations"
```

---

### Task 9: The Index action

**Files:**
- Modify: `serve.go`
- Test: `serve_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1-8
- Produces: `(*SPAController).Index(c *raptor.Context) error`, `(*SPAController).write(c *raptor.Context, e *entry)`

- [ ] **Step 1: Write the failing tests**

Append to `serve_test.go` (add imports `bytes`, `compress/gzip`, `io`, `strings`, `github.com/andybalholm/brotli`):

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./... -run TestIndex -v`
Expected: FAIL — `sc.Index` undefined

- [ ] **Step 3: Add `Index` and `write` to `serve.go`**

Add imports: `bytes`, `github.com/go-raptor/raptor/v4`, `github.com/go-raptor/raptor/v4/core`, `github.com/go-raptor/raptor/v4/errs`.

```go
// Index serves one file from the in-memory build, or the SPA shell for a
// client-side route. A request path is only ever a map key here — nothing
// derived from the request reaches the filesystem, so there is no traversal
// to defend against.
func (sc *SPAController) Index(c *raptor.Context) error {
	req := c.Request()

	// The catch-all route matches every method, but a build artifact is only
	// ever readable.
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		c.Response().Header().Set(core.HeaderAllow, "GET, HEAD")
		return errs.NewErrorMethodNotAllowed("Method " + req.Method + " not allowed")
	}

	e, ok := sc.lookup(normalizeKey(req.URL.Path))
	if !ok {
		// Handing the shell to a request for a script or stylesheet turns a
		// missing asset into an HTML parse error somewhere further along,
		// which is far harder to diagnose than a 404.
		if !isNavigation(req) {
			return c.NotFound()
		}
		e = sc.fallback
	}

	sc.write(c, e)
	return nil
}

func (sc *SPAController) write(c *raptor.Context, e *entry) {
	req := c.Request()
	res := c.Response()
	header := res.Header()

	header.Set(core.HeaderContentType, e.contentType)
	header.Set(core.HeaderCacheControl, e.cacheControl)

	rep := &e.identity
	if e.compressed() {
		// The body now depends on the request's Accept-Encoding, so any
		// shared cache has to key on it.
		header.Add(core.HeaderVary, core.HeaderAcceptEncoding)
		switch negotiateEncoding(req.Header.Get(core.HeaderAcceptEncoding), e.brotli != nil, e.gzip != nil) {
		case encodingBrotli:
			rep = e.brotli
			header.Set(core.HeaderContentEncoding, encodingBrotli)
		case encodingGzip:
			rep = e.gzip
			header.Set(core.HeaderContentEncoding, encodingGzip)
		}
	}
	header.Set(headerETag, rep.etag)

	// ServeContent handles conditional requests against the ETag set above,
	// plus Range and HEAD. The empty name stops it re-deriving a Content-Type
	// we have already resolved deterministically.
	http.ServeContent(res, req, "", e.modTime, bytes.NewReader(rep.data))
}
```

- [ ] **Step 4: Run the full suite**

Run: `go test ./... -v`
Expected: PASS, all tests

- [ ] **Step 5: Vet and race-check**

Run: `go vet ./... && go test ./... -race`
Expected: no vet findings, no race reports. The index is written once in `Setup` and only read afterwards, so concurrent requests must be clean.

- [ ] **Step 6: Commit**

```bash
git add serve.go serve_test.go
git commit -m "feat(spa): serve from memory with negotiated encoding and conditional requests"
```

---

### Task 10: Benchmark and documentation

**Files:**
- Create: `bench_test.go`
- Modify: `../README.md`

**Interfaces:**
- Consumes: everything from Tasks 1-9
- Produces: nothing consumed by later tasks

- [ ] **Step 1: Write the benchmark**

Create `bench_test.go`:

```go
package spa

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-raptor/raptor/v4/core"
)

func benchmarkServe(b *testing.B, target, acceptEncoding string) {
	b.Helper()
	dir := b.TempDir()
	writeBenchFile(b, dir+"/index.html", "<!doctype html><title>shell</title>")
	writeBenchFile(b, dir+"/_app/immutable/app.js", jsSource)

	sc := NewSPAController(SPAConfig{Directory: dir})
	sc.Init(benchCore(b).Resources)
	if err := sc.Setup(); err != nil {
		b.Fatalf("Setup: %v", err)
	}
	testCore := benchCore(b)

	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Sec-Fetch-Dest", "document")
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		rec := httptest.NewRecorder()
		if err := sc.Index(core.NewContext(testCore, req, rec)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkServeAssetIdentity(b *testing.B) {
	benchmarkServe(b, "/_app/immutable/app.js", "")
}

func BenchmarkServeAssetBrotli(b *testing.B) {
	benchmarkServe(b, "/_app/immutable/app.js", "gzip, br")
}

func BenchmarkServeShellFallback(b *testing.B) {
	benchmarkServe(b, "/dashboard/settings", "gzip, br")
}
```

Add these two helpers to `bench_test.go` (the `testing.TB` forms the benchmark needs, since the helpers in `spa_test.go` take `*testing.T`):

```go
func benchCore(b *testing.B) *core.Core {
	b.Helper()
	resources := core.NewResources()
	resources.SetLogHandler(slog.NewTextHandler(io.Discard, nil))
	resources.SetConfig(config.NewConfigDefaults())
	return core.NewCore(resources)
}

func writeBenchFile(b *testing.B, path, content string) {
	b.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		b.Fatal(err)
	}
}
```

Imports for `bench_test.go`: `io`, `log/slog`, `net/http`, `net/http/httptest`, `os`, `path/filepath`, `testing`, `github.com/go-raptor/raptor/v4/config`, `github.com/go-raptor/raptor/v4/core`.

- [ ] **Step 2: Run the benchmarks**

Run: `go test ./... -bench . -benchmem -run '^$'`
Expected: three benchmarks report. Record the ns/op and allocs/op in the commit message — serving should involve no file I/O and only the allocations `httptest` and `ServeContent` themselves make.

- [ ] **Step 3: Document the controller**

Replace `/home/h00s/dev/go/go-raptor/controllers/README.md` with:

````markdown
![Raptor](https://static.husak.me/img/raptor/logo.png)

# Raptor controllers

Ready-made controllers for the [Raptor](https://github.com/go-raptor/raptor) web framework.

## spa

Serves a single-page application — a SvelteKit `adapter-static` build, or any
equivalent — from the same server as your API.

```bash
go get github.com/go-raptor/controllers/spa
```

```go
import "github.com/go-raptor/controllers/spa"

spaController := spa.NewSPAController(spa.SPAConfig{
    Directory: "build",
})

raptor.New(&raptor.Components{
    Controllers: raptor.Controllers{spaController},
}, router.CollectRoutes(
    router.Scope("/api", apiRoutes),
    router.Get("/", "SPA.Index"),
    router.Get("/{path...}", "SPA.Index"),
))
```

### How it works

The entire build directory is read into memory at startup, compressed with
brotli and gzip, and hashed for ETags. Serving a request is a map lookup, so
no request ever touches the filesystem — which also means a rebuilt frontend
needs a server restart to be picked up.

### Caching

| Files | `Cache-Control` |
|---|---|
| `/_app/immutable/**` | `public, max-age=31536000, immutable` |
| `*.html` | `no-cache` |
| everything else | `public, max-age=3600, must-revalidate` |

### Unmatched paths

The SPA shell is returned only for page navigations, detected via
`Sec-Fetch-Dest` and falling back to `Accept: text/html`. Requests for
subresources that do not exist get a 404, so a missing chunk fails as a
missing chunk instead of arriving as HTML. Note that `curl /some/route`
sends neither header and will get a 404; add `-H 'Accept: text/html'` to
see what a browser would.

### Configuration

| Field | Default | Purpose |
|---|---|---|
| `Directory` | *(required)* | Built frontend to serve |
| `IndexFile` | `index.html` | Shell served for client-side routes |
| `ImmutablePrefixes` | `["/_app/immutable/"]` | URL prefixes holding content-hashed assets |
| `ImmutableCacheControl` | `public, max-age=31536000, immutable` | Applied under those prefixes |
| `DocumentCacheControl` | `no-cache` | Applied to `.html` |
| `AssetCacheControl` | `public, max-age=3600, must-revalidate` | Applied to everything else |
| `ServeDotfiles` | `false` | Index dot-prefixed files and directories |
| `MaxFileSize` | `0` (no limit) | Skip files above this size |
| `MinCompressSize` | `1024` | Smallest file worth compressing |

### Security headers

This controller sets caching and encoding headers only. `X-Content-Type-Options`,
`Content-Security-Policy`, `X-Frame-Options` and `Referrer-Policy` are the
responsibility of middleware, and are **not** currently set by anything in
this ecosystem — configure them yourself.
````

- [ ] **Step 4: Verify everything still passes**

Run: `go test ./... && go vet ./...`
Expected: PASS, no findings

- [ ] **Step 5: Commit**

```bash
git add bench_test.go ../README.md
git commit -m "test(spa): add serving benchmarks and document the controller"
```

---

### Task 11: Migrate the calling application

**Files:**
- Modify: the user's application, wherever `NewSPAController` is called

**Interfaces:**
- Consumes: `NewSPAController(config SPAConfig)` (Task 1)
- Produces: nothing

- [ ] **Step 1: Find the call sites**

```bash
grep -rn "NewSPAController" ~/dev --include="*.go" 2>/dev/null
```

- [ ] **Step 2: Update each one**

The signature changed from two strings to a config struct:

```go
// Before
spa.NewSPAController("build", "index.html")

// After
spa.NewSPAController(spa.SPAConfig{Directory: "build"})
```

`IndexFile` only needs setting when it is not `index.html`.

- [ ] **Step 3: Build the application**

Run `go build ./...` in the application directory.
Expected: compiles. If the app resolves the module through the proxy rather than a `replace` directive, add one while testing:
`go mod edit -replace github.com/go-raptor/controllers/spa=/home/h00s/dev/go/go-raptor/controllers/spa`

- [ ] **Step 4: Verify the app serves the frontend**

Start the app and check the three behaviours that changed:

```bash
curl -sI localhost:3000/                                    # 200, Cache-Control: no-cache
curl -sI -H 'Accept-Encoding: br' localhost:3000/_app/immutable/<a-real-file>.js
                                                            # 200, Content-Encoding: br, immutable
curl -sI localhost:3000/_app/immutable/does-not-exist.js    # 404, not the HTML shell
curl -sI -H 'Accept: text/html' localhost:3000/some/route   # 200, the shell
```

- [ ] **Step 5: Commit the application change**

```bash
git commit -am "chore: migrate to spa.SPAConfig constructor"
```

---

## Self-Review

**Design coverage.** Every decision from the Design Summary maps to tasks: RAM index (5, 6), brotli+gzip at startup (3, 5), no security headers (asserted in Task 9's `TestIndexSetsNoSecurityHeaders`), navigation-only fallback (8, 9). Every issue from the original analysis is closed: symlink escape (Task 5), dead traversal guard (removed; Task 9 tests the replacement), method gate (9), dotfiles (5), dropped `filepath.Abs` error (1), `Cache-Control` (4), compression (3), ETag (3, 9), per-request syscalls (5), HTML-for-missing-assets (9).

**Placeholders.** None. Every code step carries the code; every test step carries the assertions.

**Type consistency.** `entry` fields (`identity`, `gzip`, `brotli`, `contentType`, `cacheControl`, `modTime`) are defined in Task 3 and used unchanged in 5, 8 and 9. `etagFor(base, encoding)` is defined in 3 and called in 5. `candidate` is defined in 5 and consumed by `buildEntry`/`precompressedSibling` in the same task. `negotiateEncoding` (7) is called in 9 with the argument order it declares. Test helpers (`buildDir`, `writeFile`, `newController`, `do`, `navRequest`, `jsSource`, `testCore`) are defined once in Task 1's `spa_test.go`; the benchmark declares its own `*testing.B` variants because the shared ones take `*testing.T`.

**One known ordering wrinkle.** After Task 1 the package will not compile until Task 5 defines `entry` and `buildIndex`, because `Setup` references them. This is called out in Task 1 Step 6 and resolved at Task 6 Step 2. An implementer running the full suite between those points should expect that specific failure and no other.
