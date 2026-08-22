package spa

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// Optional exists for development, where the frontend has not been built yet
// and a fatal Setup would take the API down with it.
func TestSetupBootsWithoutABuildWhenOptional(t *testing.T) {
	tests := []struct {
		name string
		dir  func(t *testing.T) string
	}{
		{"no directory at all", func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "nope")
		}},
		{"empty directory", func(t *testing.T) string {
			return t.TempDir()
		}},
		{"directory without an index file", func(t *testing.T) string {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "app.js"), jsSource)
			return dir
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := newController(t, tt.dir(t), func(c *SPAConfig) { c.Optional = true })

			if sc.fallback != nil {
				t.Error("fallback is set without a build")
			}
			if len(sc.index) != 0 {
				t.Errorf("kept %d files with no shell to serve them under", len(sc.index))
			}
		})
	}
}

// Optional must be inert when there is a build; it only widens what Setup
// tolerates.
func TestSetupIndexesTheBuildWhenOptional(t *testing.T) {
	sc := newController(t, buildDir(t), func(c *SPAConfig) { c.Optional = true })

	if len(sc.index) != 3 {
		t.Errorf("indexed %d files, want 3", len(sc.index))
	}
	if sc.fallback != sc.index["/index.html"] {
		t.Error("Optional changed which entry became the shell")
	}
}

// Optional means "not built yet", not "misconfigured", so the failures that
// describe a wrong Directory must survive it.
func TestSetupStillFailsOnBadDirectoryWhenOptional(t *testing.T) {
	tests := []struct {
		name      string
		directory func(t *testing.T) string
		want      string
	}{
		{"unset", func(t *testing.T) string { return "" }, "Directory is required"},
		{"a regular file", func(t *testing.T) string {
			file := filepath.Join(t.TempDir(), "notadir")
			writeFile(t, file, "x")
			return file
		}, "is not a directory"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := NewSPAController(SPAConfig{Directory: tt.directory(t), Optional: true})
			sc.Init(testCore(t).Resources)

			err := sc.Setup()
			if err == nil {
				t.Fatal("Setup succeeded")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %v does not mention %q", err, tt.want)
			}
		})
	}
}

// A directory that exists but cannot be read is a broken deployment, not an
// unbuilt one, which is why the tolerance is narrowed to fs.ErrNotExist.
func TestSetupFailsOnUnreadableDirectoryWhenOptional(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "build")
	writeFile(t, filepath.Join(dir, "index.html"), "<!doctype html>")
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Skipf("chmod unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	if _, err := os.Stat(dir); err == nil {
		t.Skip("directory permissions are not enforced here")
	}

	sc := NewSPAController(SPAConfig{Directory: dir, Optional: true})
	sc.Init(testCore(t).Resources)

	if err := sc.Setup(); err == nil {
		t.Fatal("Setup tolerated an unreadable directory")
	}
}
