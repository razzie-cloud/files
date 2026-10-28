# Files

A private web file manager for S3-compatible storage, including Backblaze B2.
Named directories map directly to a bucket and optional prefix. PostgreSQL stores
configuration; object contents remain in S3. There is no local sync or mount.

The Go service embeds **rclone v1.75.2** as a library. It composes the S3 backend
with optional rclone `crypt`, with no subprocess, RC API, or custom encryption.
The executable embeds HTMX 2.0.8, PicoCSS 2.1.1, templates, and local assets.

## Authentication

All public routes must run behind access-gateway forward authentication. Files
trusts `X-Auth-Request-User` (canonical UUID) for ownership and
`X-Auth-Request-Name` for display. It has no account database or login endpoint.
The proxy must remove incoming identity headers and replace them with verified
gateway headers. Do not publish the backend port or permit untrusted peers on
its private network. Internal `/healthz` and `/readyz` do not require identity;
the public proxy must protect these paths too.

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

## Configuration

Each environment variable also has a kebab-case CLI flag, for example
`POSTGRES_URI_FROM` corresponds to `--postgres-uri-from`.

| Variable | Meaning |
| --- | --- |
| `SERVICE_PORT` | Listener port, default `8080` (`--port`) |
| `ORIGIN` | Required exact public files origin, no path |
| `POSTGRES_URI_FROM` | Normal deployment: `http://database-broker:8080/v1/instances/postgres/files/uri` |
| `POSTGRES_URI` | Direct development/recovery override; set exactly one database option |
| `POSTGRES_PASSWORD_FILE` | Optional file overriding the URI's password |
| `SECRETS_DIR` | Required persistent private secret volume |
| `MAX_UPLOAD_BYTES` | Plaintext per-file limit, default 1 GiB |
| `MAX_CONCURRENT_UPLOADS` | Per-process upload slots, default four |
| `MAX_LIST_ENTRIES` | Entries collected per directory, default 10,000 |
| `S3_ALLOWED_PRIVATE_ENDPOINTS` | Comma-separated exact host:port exceptions, empty by default |
| `S3_ALLOW_HTTP` | Default false; HTTP also requires an explicit endpoint exception |

Startup fetches a broker-provisioned database URI with bounded retries, validates
the response, connects, and applies embedded Goose migrations under a PostgreSQL
session lock. Broker errors do not expose credentials or start a partial server.
Files needs no Redis: gateway sessions stay in the gateway.

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
rclone. Set the encryption password, optional salt password, filename encryption,
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
Sharing, previews, recursive deletion, rename/move, full-text search, and resumable
browser uploads are outside this first release.

## Secret storage and recovery

S3 credentials and crypt passwords are stored outside PostgreSQL as immutable
owner-scoped files. The dedicated service user owns a 0700 directory and 0600 files.
These permissions do not encrypt files. Use deployment-managed encrypted storage
or a protected secret volume; there is no custom credential-encryption scheme.
Never render saved secrets into edit forms. Blank credential fields preserve
existing values; clearing a session token is explicit.

Secret replacements write/sync a new file before updating the database reference.
Old/unreferenced files are retained to preserve in-flight requests and recovery
after mapping deletion. Back up the PostgreSQL database and secret volume together.
Retain recovery secrets until their associated remote data is safely recoverable
elsewhere; do not delete orphan-looking files automatically. A failed metadata
write can leave an unused secret file. Offline cleanup should compare references
against a consistent database backup and the operator's crypt-recovery inventory.

Run one replica with a local secret volume. Multiple replicas require shared
protected storage with consistent permissions and atomic file creation, and
shared PostgreSQL. Keep storage anchored to its node until this is configured.

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
paths, identity, read-only enforcement, secret persistence, and discovery failures.
Tests use no production Docker context or cloud credentials.

Build the standalone image with `docker build -t files:local .`. The runtime runs
as UID/GID 10001. Ensure a bind-mounted secret directory is owned by that user.
The CI workflow runs build, vet, race tests with disposable PostgreSQL, and an
image build. Images and this repository must be published separately before
publishing a parent pointer or deploying a Swarm release.
