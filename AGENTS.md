# Files development

This is the independent `github.com/razzie-cloud/files` Go 1.27 repository and a
submodule of Razzie Cloud. Keep service changes here and deployment changes in infra.

Use rclone's Go S3 and crypt APIs. Never shell out to rclone, implement custom
encryption, or expose rclone RC. Preserve the guarded egress proxy: validating
an endpoint form alone cannot prevent DNS rebinding or redirect-based SSRF.
Initialize rclone helpers before requests and isolate every crypt cache alias.

Frontend: embedded HTMX and PicoCSS only; retain vendored licenses. No SPA or
frontend build pipeline. Keep CSP-compatible settings and escaped templates.
The main HTMX fragment includes sidebar navigation and the current view; the
public share shell never includes management links. Layout/theme rules live in
`internal/web/static/app.css` and follow the browser's light/dark preference.
Use PicoCSS switches (`type="checkbox" role="switch"`) for every binary toggle,
including confirmations and Cast options. Exclude switches from text-input sizing
rules so Pico retains their dimensions and focus/checked styling.

User identity comes only from proxy-supplied X-Auth-Request-User UUIDs. Every
metadata lookup is owner-scoped. Management traffic must pass through the gateway.
Public shares use a separate SHARE_ORIGIN hostname and capability-only router;
never accept proxy identity there. Only the random share-token lookup is unscoped;
use its persisted owner for all subsequent metadata and secret reads. Shares are
immutable and bound to directory IDs with deletion cascading; never resolve a
share by directory name or physical S3 location. Public operations are read-only.
Optional share expiration is checked on every public request, including cache hits
and password unlock; session cookies must not grant access past that deadline.
Keep the fixed 30-second bounded snapshot cache, fail closed after expiry, and
invalidate locally on management changes. Password sessions are share-scoped,
signed, host-only cookies; never render passwords, hashes, or signing keys.
Enforce read-only access before opening storage, and recheck after write locks.
Store secrets as owner-scoped PostgreSQL records; keep them out of rendered forms
and logs. Retain crypt recovery secrets when deleting mappings. Config deletion
must never remove S3 objects. Runtime needs no secret directory or encryption key.
Keep the read-only legacy importer transactional and never overwrite/delete old files.

Run `gofmt`, `go build ./...`, `go vet ./...`, `go test ./...`, and
`go test -race ./...`. Tests start in-memory S3 HTTP servers. PostgreSQL tests
use `TEST_DATABASE_URL` only on loopback and create/drop random schemas. Without
that variable they skip. Never point tests at production or use a production
Docker daemon. Add migrations rather than changing deployed schema migrations.

Do not push images, publish the repository, or deploy without authorization.
