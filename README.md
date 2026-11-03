# Files

A private web file manager for S3-compatible storage, including Backblaze B2.
Map a bucket or prefix to a named directory, then browse, upload, download, and
preview files. Directories can be read-only or writable, with optional rclone
crypt encryption. Share files and folders through read-only links with optional
passwords and expiration.

File contents stay in S3. PostgreSQL stores configuration, S3 credentials, and
crypt passwords. Files uses access-gateway for sign-in and user identity; it has
no separate login system or local file sync.

## Try it locally

Use the [razcloud local quickstart](https://github.com/razzie-cloud/.github#try-it-locally)
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
