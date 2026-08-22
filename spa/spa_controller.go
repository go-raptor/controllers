// Package spa serves a single-page application from a directory of built
// static files. The whole build is read into memory at startup, so serving
// a request is a map lookup: nothing derived from the request ever reaches
// the filesystem.
package spa

import (
	"errors"
	"fmt"
	"io/fs"
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

	// Optional lets the controller boot when Directory holds no build,
	// instead of failing Setup — a backend in development should not be held
	// hostage by an unbuilt frontend. Every SPA route then 404s until the
	// process restarts with a build in place.
	Optional bool `yaml:"optional"`

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
// unbuilt frontend should fail at startup, not on the first request, unless
// SPAConfig.Optional trades that away.
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
		// Absence is the state Optional exists for. A permission error or a
		// broken mount is a deployment fault in either mode.
		if sc.config.Optional && errors.Is(err, fs.ErrNotExist) {
			sc.logUnbuilt(root, "directory does not exist")
			return nil
		}
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
		// A checked-in public/ holding nothing but a .gitkeep is the same
		// unbuilt state as no directory at all. Whatever else the walk found
		// goes with it: an SPA without its shell is not worth half-serving.
		if sc.config.Optional {
			sc.logUnbuilt(root, "no "+sc.config.IndexFile)
			return nil
		}
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

// logUnbuilt is the only warning an operator gets: index and fallback stay
// nil, nothing re-reads the directory, and the process serves 404s until it
// is restarted.
func (sc *SPAController) logUnbuilt(root, reason string) {
	sc.Log.Warn("SPA build not found, every route will 404 until restart",
		"directory", root,
		"reason", reason,
	)
}

// hasBuild reports whether Setup found a shell to serve. Only ever false
// under SPAConfig.Optional, and fixed for the life of the process.
func (sc *SPAController) hasBuild() bool { return sc.fallback != nil }
