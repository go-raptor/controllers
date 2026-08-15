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
