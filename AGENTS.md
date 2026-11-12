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

File previews reuse the authorized download route for personal files and public
shares. Keep UTF-8 text/Markdown reads bounded to 2 MiB and escape plain text.
Goldmark must retain safe rendering defaults; raw HTML, document images, and
relative links must not run code, fetch resources, or expand a share's scope.
Document frames use script-free CSP sandboxing. Native PDF viewing allows only
same-origin framing and retains byte ranges; browser format support varies.
Audio/video playback must stop and release its source when closing, navigating
between previews, or replacing the listing through HTMX.

User identity comes only from proxy-supplied X-Auth-Request-User UUIDs.
Directory/share metadata lookups are owner-scoped.
Connections admit owned personal or shared records; only a signed gateway admin
may create/edit/remove shared connections, including the original creator. Shared
status is immutable. User-ID prefixing is shared-only and locks while any user
has an attached directory. Resolve credentials using the connection creator, crypt
secrets using the directory owner, and physical prefixes using the directory
owner UUID. Preserve database scope/prefix triggers and mapping revision checks.
Management traffic must pass through the gateway.
Public shares use a separate SHARE_ORIGIN hostname and capability-only router;
never accept proxy identity there. Only the random share-token lookup is unscoped;
use its persisted owner to resolve the directory and its available connection.
Read S3 credentials under the connection creator and crypt secrets under the
share/directory owner. Shares are
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

When ACCESS_GATEWAY_URL / --access-gateway-url is configured, initialize
internal/gatewayidentity before serving. Require X-Auth-Request-Token on every
management route, including static assets, and verify its v4.public signature,
issuer/audience/time, exact method/URI and UUID/name header equality. Cache public
keys only until expiry; discovery outages must not extend trust. Keep the verifier
and tests synchronized with the other S3 app's independent copy. Empty gateway URL
preserves trusted-proxy mode; public Files capabilities and health probes bypass
identity. Notes admin APIs also require signed admin=true and the proxy marker.

Broker discovery uses BROKER_TOKEN_FILE / --broker-token-file and optional
BROKER_CA_FILE / --broker-ca-file. Discovery URLs carry only public client IDs.
Keep internal/brokerdiscovery and its tests synchronized across first-party apps.
Never log discovery URLs or raw HTTP errors; no authentication fallback is allowed.
