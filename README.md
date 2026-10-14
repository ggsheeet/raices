# Raices

Barebones Go web stub. Layout and conventions live in `GG_TO_GO.md`.

`/` redirects to `/login`. The form checks that the email looks like an email and the password is at least 8 characters, then redirects to `/register`. Nothing is stored and there is no session.

## Run

```sh
make dev
```

Open http://localhost:3000. `make dev` sets `ENVIRONMENT=development`, which loads `.env.dev`.

## Cloud Run

Do not mount a Cloud Storage bucket as the live SQLite file.

Deploy one instance with `--max-instances 1`, `--execution-environment gen2`, and `--no-cpu-throttling`.

`LITESTREAM_REPLICA_URL` is optional. When set, the entrypoint restores only if the local SQLite file is missing, then replicates. When empty, the entrypoint execs `/raices`. Creating the replica bucket is a human deploy step.

Boot requires non-empty `SQLITE_PATH`, `APP_BASE_URL`, `TICKET_PRICE`, `MP_ACCESS_TOKEN`, `MP_PUBLIC_KEY`, `MP_WEBHOOK_SECRET`, `RESEND_API_KEY`, `RESEND_FROM`, `ADMIN_EMAIL`, and `ADMIN_PASSWORD`. `TICKET_PRICE` is whole pesos.

## Other targets

- `make build` writes a production binary to `bin/raices` with assets embedded
- `make docker-dev` and `make docker-prod` use the matching compose file
