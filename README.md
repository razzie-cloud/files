# Files

A private web file manager for S3-compatible storage, including Backblaze B2.
Map a bucket or prefix to a named directory, then browse, upload, download, and
preview files. Directories can be read-only or writable, with optional rclone
crypt encryption. Share files and folders through read-only links with optional
passwords and expiration.

Open text, formatted Markdown, PDFs, images, audio, and video directly in the
file preview dialog. Media format support depends on your browser.

File contents stay in S3. PostgreSQL stores configuration, S3 credentials, and
crypt passwords. Files uses access-gateway for sign-in and user identity; it has
no separate login system or local file sync.

## Try it locally

Use the [Razzie Cloud local quickstart](https://github.com/razzie-cloud/.github#try-it-locally)
with Git, GitHub SSH access, and Docker Compose. It starts Files together with
its database, gateway, and reverse proxy. This repository's `docker-compose.yml`
starts development dependencies only.

1. Create the first administrator at <http://localhost:8080> using the bootstrap
   token from the gateway logs, then sign in.
2. Open <http://files.localhost:8082> and add a **Storage connection** with your
   S3 endpoint, region, credentials, addressing mode, and Read-only or Writable access.
3. Add a directory using that connection, a bucket, and an optional prefix.
   Choose Plain S3 or rclone crypt, then open the directory to manage files.
4. Use **Share** beside an entry or **Sharing** in the toolbar to create a public link.

For disposable storage, the parent repository also provides an
[S3 demo](https://github.com/razzie-cloud/.github/blob/master/docs/development.md#disposable-s3-demo).
For encrypted directories, keep an independent copy of the crypt passwords and
settings so you can recover the data.

## Deploy it

Run Files behind access-gateway forward authentication with a private backend
port, a PostgreSQL database, and access to your S3 endpoint. The proxy must strip
incoming identity headers and replace them with the gateway's verified headers.

| Setting | Purpose |
| --- | --- |
| `ORIGIN` | Required public Files origin, e.g. `https://files.example.com` |
| `POSTGRES_URI_FROM` | Broker URI endpoint, e.g. `http://database-broker:8080/v1/instances/postgres/files/uri` |
| `POSTGRES_URI` | Direct database connection; set this or `POSTGRES_URI_FROM`, exactly one |
| `SHARE_ORIGIN` | Optional public sharing origin on a different hostname, e.g. `https://shared-files.example.com` |

Keep every management path behind authentication. The sharing hostname exposes
only `/s/*` and `/static/*` and stays outside gateway forward authentication.
Back up PostgreSQL as well as S3: the database contains credentials and recovery
passwords. Crypt encrypts objects on the server; it is not browser end-to-end encryption.

See the [reference guide](docs/reference.md) for complete proxy examples,
configuration, encryption, sharing, Cast support, upgrades, and development.
Contributor invariants are in [AGENTS.md](AGENTS.md); the
[infra repository](https://github.com/razzie-cloud/infra) provides a Swarm deployment.

The header logo and Razzie Cloud name link to `LANDING_ORIGIN`
(`--landing-origin`, default `https://razzie.cloud`). Set this to the exact
HTTP(S) origin of your landing page, without a trailing slash. The adjacent
service name opens this app. Compose and Swarm set the landing origin for their
local/public domain. Public Files shares retain their read-only navigation.

Set `ACCESS_GATEWAY_URL` (`--access-gateway-url`) to the gateway's exact origin,
for example `http://access-gateway:8080` on the private container network, to
require signed proxy identity. Startup fetches `/.well-known/paseto-keys` and
fails if discovery fails. Expired key sets refresh on use; stale keys are never
accepted. Caddy must strip incoming `X-Auth-Request-Token` and copy the gateway's
value alongside UUID/name. The service verifies the PASETO signature, issuer,
public app `ORIGIN`, expiry, method, URI and matching identity headers.
An empty setting retains trusted-proxy header mode. See the gateway's
[signed identity contract](https://github.com/razzie-cloud/access-gateway/blob/master/docs/reference.md#signed-proxy-identity).

## Broker discovery authentication

Set `BROKER_TOKEN_FILE` / `--broker-token-file` to a mounted per-client token
and add the public `?client=<client_id>` to each `*_URI_FROM` URL. The gateway
uses one token for its PostgreSQL and Valkey grants. Inline token parameters,
duplicate authentication parameters and invalid/unreadable token files fail
startup. Omitting the token-file option preserves tokenless discovery.
`BROKER_CA_FILE` / `--broker-ca-file` adds the operator CA for verified HTTPS;
normal system CA trust remains enabled. Discovery refuses redirects, limits
responses to 4096 bytes, uses five bounded attempts with five-second deadlines,
and emits diagnostics without URLs, tokens, response bodies or raw HTTP errors.
Authentication failures never retry without the configured credential.
