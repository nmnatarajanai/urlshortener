# URL Shortener

A Go URL shortener backed by PostgreSQL-compatible CockroachDB.

## Requirements

- Go 1.27 or later
- PostgreSQL or CockroachDB
- A database connection string

## Configuration

The application requires `DATABASE_URL`.

```bash
export DATABASE_URL='postgresql://user:password@host:26257/database?sslmode=verify-full'
```

The HTTP port defaults to `8080`. Set `PORT` to use another port:

```bash
export PORT=8081
```

Never commit database credentials or tokens to the repository. Use a secret
manager or a local untracked environment file for real credentials.

## Run Locally

Download dependencies, format the code, run tests, and start the service:

```bash
go mod download
gofmt -w main.go
go test ./...
go run .
```

Or configure and start on port `8081` in one command:

```bash
DATABASE_URL='postgresql://user:password@host:26257/database?sslmode=verify-full' \
PORT=8081 go run .
```

On startup, the application checks the database connection and creates the
`urls` table if it does not already exist.

## API

### Create a short URL

```bash
curl -X POST http://localhost:8081/shorten \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com"}'
```

Example response:

```json
{
  "short_url": "http://localhost:8081/b"
}
```

### List stored URLs

```bash
curl http://localhost:8081/urls
```

Example response:

```json
[
  {
    "short_url": "http://localhost:8081/b",
    "long_url": "https://example.com"
  }
]
```

### Redirect using a short code

Open the returned short URL, or use curl:

```bash
curl -i http://localhost:8081/b
```

The service returns an HTTP `302 Found` redirect to the original URL.

## Check Database Records

Using `psql`:

```bash
psql "$DATABASE_URL" -c 'SELECT id, original_url FROM urls ORDER BY id DESC;'
```

## Troubleshooting

### `DATABASE_URL environment variable is required`

Set `DATABASE_URL` before starting the application.

### `bind: address already in use`

Port `8080` is already occupied. Choose another port:

```bash
PORT=8081 go run .
```

### `405 Method Not Allowed`

Use `POST` for `/shorten` and `GET` for `/urls` and short-code redirects.

## Production Notes

This basic implementation queries the database for redirects. For a target such
as 10,000 requests per second, add Redis caching, request limits, authentication
for `/urls`, pagination, metrics, graceful shutdown, TLS, and multiple Go
instances behind a load balancer. See [Architecture.md](Architecture.md).
