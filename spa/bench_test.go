package spa

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-raptor/raptor/v4/config"
	"github.com/go-raptor/raptor/v4/core"
)

// The shared helpers in spa_test.go take *testing.T, so benchmarks need
// their own.
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

func benchmarkServe(b *testing.B, target, acceptEncoding string) {
	b.Helper()
	dir := b.TempDir()
	writeBenchFile(b, filepath.Join(dir, "index.html"), "<!doctype html><title>shell</title>")
	writeBenchFile(b, filepath.Join(dir, "_app", "immutable", "app.js"), jsSource)

	sc := NewSPAController(SPAConfig{Directory: dir})
	sc.Init(benchCore(b).Resources)
	if err := sc.Setup(); err != nil {
		b.Fatalf("Setup: %v", err)
	}
	serveCore := benchCore(b)

	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set(headerSecFetchDest, "document")
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		rec := httptest.NewRecorder()
		if err := sc.Index(core.NewContext(serveCore, req, rec)); err != nil {
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
