# finance-api

Backend of a small multi-user budget planner: incomes, expense and savings
categories, per-user target currency, automatic exchange rates. Go, SQLite,
no external services besides the rates provider. The frontend lives in
`finance-web`.

## Development

```sh
make dev     # go run, database in ./finance.db
make test    # go test ./... (SQLite in a temp file, no services needed)
make lint    # golangci-lint run
make up      # web + api in containers, web built from ../finance-web
```

Accounts are created only from the CLI, there is no sign-up:

```sh
go run ./cmd/finance admin create -login <name>      # asks for a password
go run ./cmd/finance admin password -login <name>    # change it, sessions are revoked
```

Configuration: `-db <path>` or `FINANCE_DB` (default `./finance.db`),
`FINANCE_RATES_REFRESH` (Go duration, default `5m`, `0` disables refresh).

## Deploy

The server runs two containers from published images: `api` (this repo) and
`web` (nginx with the built frontend, proxying `/api/` to `api`). Images are
built by CI on every `v*` tag and pushed to GHCR for `linux/amd64`. Nothing is
built on the server.

### Prerequisites

- Docker with the compose plugin, the deploying user in the `docker` group
- an encrypted volume mounted at `/mnt/vault`: database in
  `/mnt/vault/finance/data`, this `deploy/` directory in
  `/mnt/vault/finance/stack`
- the api container runs as uid 1000 and needs to own the data directory

### Setup

1. Create the directories (once):
   ```sh
   sudo install -d -o 1000 -g 1000 /mnt/vault/finance/data
   ```
   ```sh
   sudo install -d -o "$USER" /mnt/vault/finance/stack
   ```
2. Copy `deploy/` to `/mnt/vault/finance/stack` and create `.env` from the
   example:
   ```sh
   cp .env.example .env
   ```
3. Install the scripts:
   ```sh
   sudo cp scripts/* /usr/local/bin/
   ```
4. Pin the image tags in `docker-compose.yml` to the release you want, then
   start:
   ```sh
   finance-up
   ```
5. Create the first admin (the stack must be running):
   ```sh
   docker compose exec api finance admin create -login <name>
   ```

The web container listens on `127.0.0.1:8090` only. Publish it inside your
private network (for example with `tailscale serve`), never on a public
interface. The proxy in front must forward `X-Forwarded-Proto: https`; the api
sets the `Secure` cookie flag from it.

### Scripts

- `finance-up`: checks that `/mnt/vault` is mounted, `docker compose up -d`.
  Required after every reboot, after the volume has been opened.
- `finance-down`: `docker compose down`.
- `finance-update`: `docker compose pull`, `up -d`, prune old images. Change
  the tags in `docker-compose.yml` first; tags are pinned on purpose.
- `check-finance`: mount, container status, health and sign-in guard through
  the web container. The private-network address cannot be checked from the
  host itself (`tailscale serve` does not answer the local node), open it
  from another device.

### Data

Everything is one SQLite file, `/mnt/vault/finance/data/finance.db`, in WAL
mode. Migrations run automatically on start.

Restore or move: stop the stack, put `finance.db` into the data directory
(owned by uid 1000, without stale `-wal` and `-shm` files next to it), start.

Backup: copy the file only through a consistent snapshot, for example
`sqlite3 finance.db "VACUUM INTO 'backup.db'"` from the host, never with a
plain `cp` while the api is running.

### Update

Tag a release (`git tag v0.2.0 && git push --tags`), wait for the `image`
job, set the new tag in `docker-compose.yml` on the server, run
`finance-update`.
