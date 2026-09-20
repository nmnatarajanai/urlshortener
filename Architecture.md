# URL Shortener Architecture

## Goal

Design the service to support 10,000 requests per second with predictable latency,
controlled database load, and horizontal scaling.

The target must be validated with production-like load tests. It is not a guarantee
from application code alone.

## Target Architecture

```text
Clients
  |
  v
TLS Load Balancer
  |
  v
Multiple Go API Instances
  |                    |
  |                    +--> Metrics, logs, traces
  v
Redis Cluster <--------+
  |
  | cache miss
  v
CockroachDB/PostgreSQL
```

## Components

### Go API

- Stateless HTTP instances behind a load balancer.
- `POST /shorten` creates a database record and populates Redis.
- `GET /{shortCode}` reads Redis first and redirects without a database query on a cache hit.
- `GET /urls` is an administrative endpoint and must be authenticated, paginated, and rate-limited.
- Health endpoints should separate process health from database readiness.
- Use graceful shutdown so in-flight requests finish during deployment.

### Redis

Use Redis as the primary read path for redirects.

```text
Key:   url:{shortCode}
Value: original URL
TTL:   optional; use a long TTL or no TTL for permanent links
```

Redirect flow:

1. Validate the short code.
2. Read `url:{shortCode}` from Redis.
3. On a hit, return the redirect immediately.
4. On a miss, query the database.
5. If found, write the result to Redis and return the redirect.
6. If not found, return `404` and optionally cache the negative result briefly.

Protect against cache stampedes by using request coalescing or a Redis lock when
many requests miss the same key.

### Database

CockroachDB/PostgreSQL remains the source of truth.

```sql
CREATE TABLE urls (
    id BIGINT PRIMARY KEY DEFAULT unique_rowid(),
    original_url TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Requirements:

- Read the connection string from `DATABASE_URL` or a secret manager.
- Size the connection pool from database capacity and measured concurrency.
- Keep writes transactional and parameterized.
- Use migrations instead of creating schema during every application startup.
- Monitor query latency, connection wait time, retries, and errors.

## Request Flows

### Create Short URL

```text
Client
  -> POST /shorten
  -> Validate JSON and URL
  -> Insert into database
  -> Generate short code from returned ID
  -> SET url:{shortCode} in Redis
  -> Return short URL
```

The write path is database-bound. To scale high write traffic, use database
capacity planning, retry handling for serializable transaction conflicts, and
possibly a dedicated ID allocation strategy.

### Redirect

```text
Client
  -> GET /{shortCode}
  -> Redis GET
  -> Cache hit: HTTP 302/301 redirect
  -> Cache miss: database lookup, Redis SET, redirect
```

The redirect path should not query the database for normal cache-hit traffic.

### List URLs

`GET /urls` must never return the entire table. Require pagination such as:

```text
GET /urls?limit=100&cursor=...
```

Keep this endpoint off the public high-throughput path or protect it with admin
authentication and a separate rate limit.

## Scaling Model

- Run multiple identical Go instances across availability zones.
- Keep instances stateless so the load balancer can distribute requests freely.
- Scale Redis independently from the API.
- Scale CockroachDB based on write throughput and cache-miss throughput.
- Use connection pooling per instance while keeping total connections within the
  database cluster limit.
- Apply rate limits before expensive database operations.

An example capacity plan is four API instances handling approximately 2,500
requests per second each, subject to benchmark results.

## Reliability and Security

- Terminate TLS at the load balancer or at the Go service.
- Do not build public URLs from an untrusted `Host` header; use a configured
  `PUBLIC_BASE_URL`.
- Limit request bodies, validate URL schemes, and reject malformed input.
- Add timeouts for Redis and database operations.
- Use circuit breakers or bounded fallbacks when Redis is unavailable.
- Do not expose database credentials in source, logs, or Git history.
- Rotate any credential that has previously been exposed.
- Add structured logs with request IDs, metrics, and distributed tracing.

## Implementation Phases

1. Extract configuration: `DATABASE_URL`, `PUBLIC_BASE_URL`, Redis address, and pool limits.
2. Add URL validation, request-size limits, pagination, and admin protection.
3. Add Redis read-through caching for redirect requests.
4. Populate Redis on successful short URL creation.
5. Add graceful shutdown, readiness checks, metrics, and structured logging.
6. Deploy multiple API instances behind a TLS load balancer.
7. Run load tests and tune API, Redis, and database capacity.

## Load-Test Acceptance Criteria

Test cache hits, cache misses, creates, and mixed traffic separately. Record:

- Requests per second
- p50, p95, and p99 latency
- HTTP error rate
- Go CPU and memory
- Redis hit ratio and latency
- Database CPU, latency, retries, and connection waits

The service should only be described as supporting 10,000 requests per second after
meeting the agreed latency and error-rate targets under representative deployment
conditions.
