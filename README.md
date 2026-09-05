# finance-api

Budget planner API: incomes, expense and savings categories, per-user target
currency, exchange rates. Go, SQLite. Frontend: `finance-web`.

## Development

```sh
make dev     # go run, database ./finance.db
make test
make lint
make up      # web + api in containers
```

```sh
go run ./cmd/finance admin create -login <name>
go run ./cmd/finance admin password -login <name>
```

Flags and environment: `-db <path>` / `FINANCE_DB` (default `./finance.db`),
`FINANCE_RATES_REFRESH` (default `5m`, `0` = off).

## Deploy

Images: `ghcr.io/vesmirov/finance-api`, `ghcr.io/vesmirov/finance-web`,
built on `v*` tags, `linux/amd64`.

Layout on the server:

- `/mnt/vault/finance/data` — `finance.db`, owner uid 1000
- `/mnt/vault/finance/stack` — contents of `deploy/`

Setup:

```sh
sudo install -d -o 1000 -g 1000 /mnt/vault/finance/data
sudo install -d -o "$USER" /mnt/vault/finance/stack
cp .env.example .env
sudo cp scripts/* /usr/local/bin/
finance-up
docker compose exec api finance admin create -login <name>
```

`web` listens on `127.0.0.1:8090`. The proxy in front must send
`X-Forwarded-Proto`.

Scripts:

- `finance-up` — `docker compose up -d`, requires `/mnt/vault` mounted
- `finance-down` — `docker compose down`
- `finance-update` — `pull`, `up -d`, image prune; change the tags in
  `docker-compose.yml` first
- `check-finance` — mount, containers, health, sign-in guard

Update: push a `v*` tag, set it in `docker-compose.yml`, `finance-update`.

Backup: `sqlite3 finance.db "VACUUM INTO 'backup.db'"`. Restore: stop, replace
`finance.db` (no `-wal`/`-shm` next to it), start.
