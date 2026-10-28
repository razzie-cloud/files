# Files

A private web file manager for S3-compatible storage, including Backblaze B2.
Named directories map directly to a bucket and optional prefix. PostgreSQL stores
configuration and credentials; object contents remain in S3. There is no local sync or mount.

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
| `IMPORT_SECRETS_DIR` | Optional one-time import of a legacy secret directory (`--import-secrets-dir`) |
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
Sharing, previews, recursive deletion, rename/move, full-text search, and resumable
browser uploads are outside this first release.

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
pushed to this service repository. For example, from this directory:

```sh
git tag v1.2.3
git push origin v1.2.3
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
