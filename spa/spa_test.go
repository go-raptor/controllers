package spa

import (
	"io"
	"log/slog"
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
