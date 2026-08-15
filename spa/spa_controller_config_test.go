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
