package spa

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
)

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
