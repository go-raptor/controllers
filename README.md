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

Because request paths only ever become map keys, path traversal has nothing
to traverse. Symlinks inside the build directory are never followed, at
startup or afterwards.

### Caching

| Files | `Cache-Control` |
|---|---|
| `/_app/immutable/**` | `public, max-age=31536000, immutable` |
| `*.html` | `no-cache` |
| everything else | `public, max-age=3600, must-revalidate` |

Every response carries a strong `ETag` derived from the file's content hash,
with a distinct validator per encoding, so conditional requests answer `304`
without a body.

### Compression

Files above `MinCompressSize` whose type benefits from it are compressed once
at startup with brotli and gzip at maximum level, and the best encoding the
client accepts is served with `Vary: Accept-Encoding`. Per-request
compression CPU is zero. If your build already emits `.br`/`.gz` siblings
(adapter-static's `precompress: true`), those are adopted instead, which
skips the slowest part of boot.

### Unmatched paths

The SPA shell is returned only for page navigations, detected via
`Sec-Fetch-Dest` and falling back to `Accept: text/html`. Requests for
subresources that do not exist get a 404, so a missing chunk fails as a
missing chunk instead of arriving as HTML. Note that `curl /some/route`
sends neither header and will get a 404; add `-H 'Accept: text/html'` to
see what a browser would.

Prerendered pages resolve under either `trailingSlash` setting: `/about`
finds `about.html` or `about/index.html`.

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

A missing directory, a missing index file, or a `Directory` that is not a
directory all fail at `Setup()`, which aborts boot rather than 404ing every
page at runtime.

### Security headers

This controller sets caching and encoding headers only. `X-Content-Type-Options`,
`Content-Security-Policy`, `X-Frame-Options` and `Referrer-Policy` are the
responsibility of middleware, and are **not** currently set by anything in
this ecosystem — configure them yourself.
