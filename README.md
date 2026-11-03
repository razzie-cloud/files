# Files

A private web file manager for S3-compatible storage, including Backblaze B2.
Named directories map directly to a bucket and optional prefix. PostgreSQL stores
configuration and credentials; object contents remain in S3. There is no local sync or mount.

The Go service embeds **rclone v1.75.2** as a library. It composes the S3 backend
with optional rclone `crypt`, with no subprocess, RC API, or custom encryption.
The executable embeds HTMX 2.0.8, PicoCSS 2.1.1, templates, and local assets.

Image and video files have an **Open** action in private folders and public
shares. Previews use a native HTML dialog with images and video controls, keeping
the folder in place. Previous/Next moves through media on the current listing
page; Escape, the close button, or the backdrop closes the dialog. Video seeking
uses streamed byte ranges, including for rclone-encrypted files. Supported image
formats and video codecs depend on the browser; Download remains available when
a file cannot be displayed.

## Authentication

All management routes on `ORIGIN` must run behind access-gateway forward authentication. Files
trusts `X-Auth-Request-User` (canonical UUID) for ownership and
`X-Auth-Request-Name` for display. It has no account database or login endpoint.
The proxy must remove incoming identity headers and replace them with verified
gateway headers. Do not publish the backend port or permit untrusted peers on
its private network. Internal `/healthz` and `/readyz` do not require identity;
the management proxy must protect these paths too. The separate sharing proxy
only exposes `/s/*` and `/static/*`, returning 404 for everything else.

Example Caddy route (the gateway must allow this exact files origin):

```caddyfile
files.example.com {
    route {
        request_header -X-Auth-Request-User
        request_header -X-Auth-Request-Name
        forward_auth access-gateway:8080 {
            uri /forward_auth
            copy_headers X-Auth-Request-User X-Auth-Request-Name
        }
        reverse_proxy files:8080
    }
}
```

Protect every path, including `/_auth/callback`. The gateway handles its callback
through forward auth. Gateway sign-out revokes subsequent requests; existing
streams run until completion/cancellation. All application mutations require a
matching Origin header. There is no special admin bypass.

## Sharing

Set `SHARE_ORIGIN=https://shared-files.example.com` and point that hostname at
Caddy. Preserve the original Host header when proxying. Keep management on
`ORIGIN` behind forward auth; the share host deliberately allows unauthenticated
visitors and must not be added to the gateway's `FORWARD_AUTH_ORIGINS`.

```caddyfile
shared-files.example.com {
    route {
        request_header -X-Auth-Request-User
        request_header -X-Auth-Request-Name
        @shared path /s/* /static/*
        reverse_proxy @shared files:8080
        respond 404
    }
}
```

Use **Share** beside a file/folder, or **Sharing** in the directory toolbar.
Leave the path empty and select Directory to share the entire mapped directory.
Owners can list/copy links and revoke them from this page, including on read-only
storage connections. Public pages allow read-only browsing, downloading, previewing, and casting, with
pagination and HTTP range downloads; rclone crypt decryption works normally.
Directory shares include descendants and future contents. Links are live views:
replacing an object at the same path changes what recipients download.

Set **Expires after** to a positive whole number of minutes, or leave it blank
for no expiration. The management page shows the deadline in UTC. Expired links
return 404 for browsing, downloads, and password entry, even with cached metadata
or an existing password cookie. Password sessions end at the earlier of 12 hours
or the share deadline. Expired records remain visible to their owner for removal;
no cleanup job is required. Downloads already in flight may finish.

Each random 256-bit link is permanently bound to the owner's **directory ID** and
relative path/type. Renaming a directory keeps its shares; deleting its
configuration cascades to its shares. Recreating the same name, bucket, or prefix
does not restore them. Scope/password/expiration changes require revoking and creating a new
link. Share URLs are bearer credentials; keep them private when appropriate.

Optional passwords are stored as bcrypt hashes (maximum 72 UTF-8 bytes). A correct
password sets a host-only, HttpOnly, SameSite=Lax session cookie scoped to that
share's path, with Secure on HTTPS. Browser sessions have a maximum lifetime of
12 hours and survive service restarts/replica changes using a persisted per-share
signing key. Password checks have two concurrent slots and a process-wide limit
of two attempts per second with a burst of five. Public mutations require the
exact sharing Origin. User-supplied identity headers cannot unlock shares or
expose management routes on the sharing host.

Each replica caches share, directory, connection, and storage credentials for a
fixed **30 seconds**, with at most 1,024 entries and coalesced concurrent loads.
Refreshes reuse this snapshot without database queries; S3 listings/content stay
live. Missing links are cached for five seconds. Backend errors are not cached,
and expired snapshots are never served on errors. Management changes clear the
local cache; other replicas see revocation/deletion/credential changes within
30 seconds. Downloads already in flight may finish. Responses use `no-store` and
`no-referrer`, so HTML and passwords are not cached in the browser.

## Google Cast

Choose **Cast** beside an MP4, or **Cast slideshow** above a folder listing,
including the directory root. The sender uses Google's Default Media Receiver
without registration, transcoding, or a custom receiver. H.264 MP4 is the
compatibility baseline; HEVC depends on the receiver and the file's codecs.
JPEG/PNG slideshows include direct children across all listing pages, sorted by
filename, within `MAX_LIST_ENTRIES`. Originals are delivered unchanged; large
photos, portrait orientation, repeated image loads, and receiver idle behavior
still need real-device validation.

Use HTTPS in supported desktop/Android Chrome, with the browser and receiver on
the same reachable network. The receiver must reach the public `SHARE_ORIGIN`
host and Google services; loopback-only development URLs cannot serve a TV.
The SDK is loaded only when a casting action is shown. Local app code remains
embedded; CSP permits specific gstatic sender script paths. Media CORS permits
`https://www.gstatic.com` and configured management/share origins without
credentials. Actual SDK dependencies and receiver CORS requests remain hardware
acceptance items; do not broaden management CORS to fix receiver requests.

Device selection precedes the first share creation, so canceling creates no share.
While connected, another Cast action reuses that receiver without reopening the
session-management picker.
Authenticated casting creates one ordinary passwordless share, visible in
**Sharing**, with a server-selected `CAST_SHARE_TTL` deadline (default `6h`,
positive and at most `24h`; same configuration on every replica). Folder shares
include descendants, non-image files, and future contents even though slideshow
entries include only the current folder's pictures. Stop, replacement, or failed
startup attempts share revocation. A lost response or closed tab relies on expiry;
expired records remain visible for manual removal. Metadata creation works on
read-only directories and connections.

Existing public shares have the same actions. Unlock password-protected shares
first: the sender delegates a signed ticket for the exact video or slideshow
folder. The ticket expires by the earliest of `CAST_SHARE_TTL`, the password
session deadline, or the original share's expiry. Public casting creates no
persisted share, and Stop never revokes the owner's original share. A delegated
ticket remains valid until its deadline or parent revocation. Existing cache
rules bound cross-replica revocation to 30 seconds; delivered bytes and pictures
cannot be recalled.

The document-local controller survives HTMX navigation. Video supports normal
remote play/pause, seeking, volume, and Stop. Stop ends the owned browser/receiver
session, including an idle session; new Cast actions become available once the
SDK finishes disconnecting. Slideshows support previous/next,
retry, pause/resume, 2–300 seconds per picture (default 10), shuffle, and loop.
Only one LOAD is active; rapid selections coalesce. A failed picture pauses for
retry/skip; authorization failure stops playback. Hiding the page pauses timed
advancement; return and explicitly Resume. Reloading loses slideshow state,
and reconnection does not silently restart timers or create shares.

Protocol: form POST `/directories/{id}/cast` creates an owner playback description;
form POST `/s/{share}/cast` describes public playback. Capability GET/HEAD
`/s/{share}/media?path=...` delivers validated MP4/JPEG/PNG with ranges; GET
`/s/{share}/slideshow?path=...` returns the bounded picture manifest. Password
capabilities add a `ticket` query parameter. URLs/tickets are credentials: keep
in memory and out of logs or persistent browser storage. Header validation reads
at most 64 KiB, and does not establish full-file integrity or codec support.
Ordinary download/preview semantics are unchanged.

Sender tests: `node tests/cast-controller.test.cjs` (Node 22+). Browser adapter
checks: `python tests/cast-browser.py` with Playwright and Chromium installed.
Neither mocks nor SDK acknowledgments prove rendering on a physical receiver.

## Configuration

Each environment variable also has a kebab-case CLI flag, for example
`POSTGRES_URI_FROM` corresponds to `--postgres-uri-from`.

| Variable | Meaning |
| --- | --- |
| `SERVICE_PORT` | Listener port, default `8080` (`--port`) |
| `ORIGIN` | Required exact public files origin, no path |
| `SHARE_ORIGIN` | Optional exact public sharing origin on a different hostname (`--share-origin`); unset disables sharing |
| `POSTGRES_URI_FROM` | Normal deployment: `http://database-broker:8080/v1/instances/postgres/files/uri` |
| `POSTGRES_URI` | Direct development/recovery override; set exactly one database option |
| `POSTGRES_PASSWORD_FILE` | Optional file overriding the URI's password |
| `IMPORT_SECRETS_DIR` | Optional one-time import of a legacy secret directory (`--import-secrets-dir`) |
| `MAX_UPLOAD_BYTES` | Plaintext per-file limit, default 1 GiB |
| `MAX_CONCURRENT_UPLOADS` | Per-process upload slots, default four |
| `CAST_SHARE_TTL` | Temporary playback lifetime, default `6h`, positive and at most `24h` (`--cast-share-ttl`) |
| `MAX_LIST_ENTRIES` | Entries collected per directory, default 10,000 |
| `S3_ALLOWED_PRIVATE_ENDPOINTS` | Comma-separated exact host:port exceptions, empty by default |
| `S3_ALLOW_HTTP` | Default false; HTTP also requires an explicit endpoint exception |

Startup fetches a broker-provisioned database URI with bounded retries, validates
the response, connects, and applies embedded Goose migrations under a PostgreSQL
session lock. Broker errors do not expose credentials or start a partial server.
Files needs no Redis: gateway sessions stay in the gateway; share password sessions use signed cookies.

An authenticated loopback egress proxy inside the process enforces S3 destination
policy for rclone's HTTP clients. It validates DNS results at each new connection,
dials the validated numeric IP, and blocks private/reserved addresses and ports
other than 443 unless explicitly excepted. TLS certificate verification remains
enabled. Broker and PostgreSQL connections do not use this proxy. Do not add
wildcard/private-network exceptions for user-supplied endpoints.

## Using storage

1. Open Storage connections. Supply an S3 provider, endpoint, signing region,
   credentials, addressing mode, and an explicit Read-only or Writable choice.
2. Add a directory with its connection, bucket, and optional prefix. It may further
   restrict a writable connection to read-only. Overlapping roots on the same
   connection/bucket are rejected; separate provider credentials can still access
   overlapping data outside the application's ownership model.
3. Select Plain S3 or rclone crypt. Writable directories show an upload widget,
   folder creation, and confirmed delete/replace actions. Read-only directories
   omit those controls and reject forged mutation requests server-side.

For Backblaze, choose Backblaze B2, use
`https://s3.<your-region>.backblazeb2.com`, and enter that same signing region.
The application key ID is the access key, and the application key is the secret.
Use path-style addressing and a bucket/prefix-restricted key where possible.
Files maps this preset to rclone's generic S3 provider, not its native B2 backend.
Live Backblaze compatibility needs acceptance testing with your disposable keys;
the automated suite runs against an in-memory S3 implementation.

When using crypt, uploads encrypt and downloads decrypt automatically through
rclone. In **Add directory → Encryption → rclone crypt**, set **Encryption
password (rclone password)** and **Salt password (rclone password2, optional)**.
These belong to the directory, rather than the storage connection. Enter the
original passwords, before rclone obscures them for its config file; leave
password2 empty if the remote has no salt password. Set filename encryption
and matching advanced settings if attaching an existing rclone directory. An
existing encrypted prefix requires a known non-empty decrypted file path to verify the key;
a new prefix must be empty. Plain and crypt data must use separate prefixes.
Crypt failures never trigger a plaintext fallback. The browser sends plaintext
over HTTPS to the service; this is server-side encryption, not browser end-to-end
encryption. Keep an independent copy of crypt passwords/settings for recovery.

Directory location and encryption settings are immutable after creation. Editing
a view only changes its name/read-only restriction. Changing a crypt password
would not migrate existing objects. Make a new mapping for a different setup.
Deleting mappings or connections never deletes bucket contents or recovery secrets.

Default uploads reject existing targets. The separate replace action requires
confirmation. Advisory locks serialize service writes using physical keys across
replicas; external clients/different connections can race. There is no universal
atomic create-if-absent guarantee. Interrupted uploads can have uncertain completion;
refresh before retrying. Configure provider lifecycle cleanup of abandoned
multipart uploads. Directory deletion is empty-only; bucket deletion is forbidden.

Listings are bounded and paginated 100 entries per page. A directory over the
entry limit produces an explicit error, not a silently truncated view. Refreshes
can re-list remotely. Downloads support one byte range, including through crypt.
Previews, recursive deletion, rename/move, full-text search, and resumable
browser uploads are not supported.

## Secret storage and recovery

S3 credentials and rclone crypt passwords are immutable, owner-scoped records in
PostgreSQL's `service_secrets` table. Connection and directory references have
composite foreign keys that prevent cross-owner references. A PostgreSQL backup
includes both configuration and recovery secrets; there is no runtime secret
volume, host directory, or separately managed encryption key.

Secret values are readable JSONB inside the service's database. No custom
application encryption or reversible obscuring is presented as encryption at rest.
Use the deployment's PostgreSQL storage/backup encryption if desired. Disclosure
of this database includes the S3 credentials and crypt passwords. Rclone crypt
still encrypts file objects in S3; it does not protect against database disclosure.

Never render saved secrets into edit forms. Blank credential fields preserve
existing values; clearing a session token is explicit. Replacements create new
records before updating configuration references. Old/unreferenced records are
retained for in-flight requests and recovery after a mapping is removed. Deleting
configuration never deletes remote objects or their recovery secrets. Clean up
retained records only after confirming that the remote data is recoverable elsewhere.

Replicas share PostgreSQL and need no shared files volume. Keep proxy-to-service
identity trust enforced by network isolation; moving secrets into PostgreSQL does
not change that separate boundary.

### Upgrading an existing file-backed installation

Stop the old files instances and back up their database and secret volume. Start
one new instance with the old volume mounted **read-only**, and set
`IMPORT_SECRETS_DIR` to that mount path (or use `--import-secrets-dir`). The new
migration creates the database store while retaining existing reference IDs.
Startup imports every record, including retained recovery passwords, in a single
transaction and validates the references before listening. Missing files, malformed
records, or conflicts fail startup without committing a partial import or printing
secret values. Repeating the import is safe and never overwrites an existing value.

After successful import, remove the import option and legacy volume mount and
restart normally. Keep the old backup until the new database backup and recovery
paths are verified; the importer never deletes or changes the old files. Fresh
installations do not need this step. `SECRETS_DIR` and `--secrets-dir` are no longer
runtime configuration options. Rolling back to the old binary requires its original
secret volume as well as the appropriate database backup.

## Development and checks

Use Go 1.27. From the parent razcloud checkout, the root Compose/Caddy stack
provides broker discovery, gateway sign-in, and the files route. For disposable
standalone dependencies, set `TEST_POSTGRES_PASSWORD` and `TEST_S3_PASSWORD`, then
run `docker compose up -d` in this repository against a local Docker daemon.
PostgreSQL is loopback port 55432; the in-memory S3 fixture is API port 59000,
with access key `files-dev`, your test password, and pre-created bucket `files-demo`.
The standalone Compose file only supplies development dependencies, not an
unauthenticated public service. The fixture's objects disappear on restart; it is
implemented in Go with the pinned S3 test library, not an rclone subprocess.

```sh
gofmt -w cmd internal
go build ./...
go vet ./...
TEST_DATABASE_URL='postgres://files_test:LOCAL_PASSWORD@127.0.0.1:55432/files_test?sslmode=disable' go test ./...
TEST_DATABASE_URL='postgres://files_test:LOCAL_PASSWORD@127.0.0.1:55432/files_test?sslmode=disable' go test -race ./...
```

Without `TEST_DATABASE_URL`, PostgreSQL and HTTP workflow integration tests skip.
They require loopback and create/drop random schemas. Other tests run real rclone
against a test S3 HTTP server and check crypt bytes, range reads, wrong keys,
paths, identity, read-only enforcement, database secret persistence, legacy imports,
and discovery failures.
Tests use no production Docker context or cloud credentials.

Build the standalone image with `docker build -t files:local .`. The runtime runs
as UID/GID 10001 and needs no writable persistent filesystem volume.
The Checks workflow runs build, vet, race tests with disposable PostgreSQL, and
an image build. Publish the service commits and tagged image before publishing
a parent pointer or deploying a Swarm release.

## Publish an image

The `Publish image` workflow builds the tagged commit's Dockerfile for
`linux/amd64` and pushes `ghcr.io/razzie-cloud/files:<git-tag>` when a new tag is
pushed to this service repository. Set `RELEASE_TAG` to your chosen release tag,
then run from this directory:

```sh
git tag "${RELEASE_TAG:?Set RELEASE_TAG}"
git push origin "$RELEASE_TAG"
```

The tagged commit must include `.github/workflows/publish-image.yml`. Creating
a new tag through GitHub's release UI also triggers the workflow. Tags in the
parent razcloud repository do not trigger service builds.

The image tag exactly matches the Git tag, including any `v` prefix; no `latest`
alias is added. Use Docker-compatible tags: 1–128 ASCII letters, digits,
underscores, dots, or hyphens, starting with a letter, digit, or underscore.
Incompatible tags fail before publication.

Authentication uses the built-in `GITHUB_TOKEN` with `packages: write`; no custom
registry secret is needed. Organization policy must allow package creation;
existing packages must grant this repository Actions write access. Set package
visibility to public for unauthenticated pulls, or authenticate deployment hosts.

## Health probes

Internal `GET /healthz` reports process liveness; `GET /readyz` reports readiness
of PostgreSQL.
Both support HEAD and return plain text: 200 on success, 503 when not ready,
and 405 for other methods. Responses use `Cache-Control: no-store` and never
include backend addresses, credentials, or error details.

Readiness checks run immediately in a background worker and repeat ten seconds
after each cycle, with a two-second deadline per dependency. Until the first
successful cycle, after a failed cycle, or once the last result is thirty seconds
old, readiness returns 503. Probes only read cached state; they never query a
backend, provision an instance, or generate request logs. Failed background
checks log dependency failure and recovery transitions without raw backend errors.
User-configured S3 endpoints are not part of service readiness.

Use these endpoints directly on the private backend port. Keep public ingress
and authentication rules in place. Container health checks use `/readyz` on
`SERVICE_PORT` (default 8080).

## Logging

Logs are JSON records written to stderr using Go's `log/slog`. Each record has
`time`, `level`, `msg`, `service`, and a stable `event` name. `LOG_LEVEL` controls
verbosity independently of service configuration: `DEBUG`, `INFO` (default),
`WARN`, or `ERROR`, case-insensitive. Invalid values fail startup.

Operational failures include a generated `request_id` (also returned as
`X-Request-ID`), method, static route template, status, duration in milliseconds,
operation, and a safe `error_kind`. Known database errors include their SQLSTATE;
raw backend error messages and SQL are excluded. Request URLs, query strings,
cookies, bodies, credentials, and panic values are not logged. Panic diagnostics
include code locations only.

Routine requests are quiet at INFO. DEBUG enables request summaries; static
assets and health probes remain quiet. Dependency outages emit one
`dependency.unavailable` WARN and a `dependency.recovered` INFO with outage
duration, rather than repeating a warning every probe cycle. Normal canceled
requests and interrupted client downloads are DEBUG events.

Connection/directory changes, share creation/revocation, and legacy secret
imports produce INFO events with owner/resource IDs or counts. Share IDs are
capability tokens and are excluded, together with object names, bucket names,
prefixes, and remote endpoints. Upload and storage-mutation summaries are DEBUG.
Raw rclone logging remains disabled. The development S3 fixture uses the same
JSON format and `LOG_LEVEL` setting.
