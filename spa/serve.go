package spa

import (
	"bytes"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/go-raptor/raptor/v4"
	"github.com/go-raptor/raptor/v4/core"
	"github.com/go-raptor/raptor/v4/errs"
)

const (
	encodingBrotli     = "br"
	encodingGzip       = "gzip"
	headerETag         = "ETag"
	headerSecFetchDest = "Sec-Fetch-Dest"
)

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
