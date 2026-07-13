# Load Testing Results (wrk)

Benchmarks run against the full local Docker Compose stack: client → nginx → Go app → Redis/Postgres.

**Tool:** [wrk](https://github.com/wg/wrk)  
**Machine:** Linux, 15.4 GB RAM  
**Stack:** nginx (alpine), Go app, Redis 7, Postgres 17 — all in Docker Compose on a single host

---

## Setup

To reproduce these results, first bring up the stack:

```bash
docker compose up --build -d
```

Install wrk:

```bash
sudo apt install wrk   # Debian/Ubuntu
brew install wrk       # macOS
```

Register a user, create a short URL, and warm the Redis cache:

```bash
TOKEN=$(curl -s -X POST http://localhost/register \
  -H "Content-Type: application/json" \
  -d '{"email":"bench@test.local","password":"benchpass"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")

SHORT=$(curl -s -X POST http://localhost/urls \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"long_url":"https://example.com"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['short_url'])")

# Warm the Redis cache with one request
curl -s -o /dev/null http://localhost/$SHORT
```

> **Rate limit note:** The app defaults to 20 req/min per IP, which would cap wrk well below actual throughput. For benchmarking, temporarily raise it in `main.go` before rebuilding:
> ```go
> rl := rateLimitMiddleware(rdb, 1000000, time.Minute)
> ```
> Restore it to 20 when done.

---

## Results

### Application Layer (Go app direct on :8080, bypassing nginx)

This isolates the performance of the Go + Redis stack itself.

```
wrk -t4 -c100 -d20s http://localhost:8080/<shortCode>
```

| Metric | Value |
|---|---|
| Requests/sec | **36,082** |
| Avg latency | **2.78ms** |
| Max latency | 14.64ms |
| Total requests (20s) | 722,159 |
| Errors | 0 |

```
Running 20s test @ http://localhost:8080/1
  4 threads and 100 connections
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency     2.78ms    1.14ms  14.64ms   71.99%
    Req/Sec     9.07k   319.10    10.03k    71.12%
  722159 requests in 20.01s, 132.23MB read
Requests/sec:  36082.54
Transfer/sec:      6.61MB
```

This measures the hot redirect path: Redis GET → 302 redirect. No Postgres involved on cache hits.

---

### Full Stack (client → nginx → Go → Redis)

```
wrk -t4 -c100 -d20s http://localhost/<shortCode>
```

| Metric | Value |
|---|---|
| Requests/sec | **1,223** |
| Avg latency | **85ms** |
| Total requests (20s) | 24,499 |
| Errors | 0 |

```
Running 20s test @ http://localhost/1
  4 threads and 100 connections
  Thread Stats   Avg      Stdev     Max   +/- Stdev
    Latency    85.25ms   69.10ms 216.85ms   32.26%
    Req/Sec   307.67    570.59     2.64k    91.99%
  24499 requests in 20.03s, 5.56MB read
Requests/sec:   1223.25
Transfer/sec:    284.31KB
```

The nginx gap here is larger than it would be in production because the default `events {}` config uses a single worker. Updated to `worker_processes auto` during this session, which improved throughput. A tuned nginx in production (multiple workers, connection pooling to upstream) would narrow this significantly.

---

### Health Endpoint Baseline

The `/health` endpoint pings both Postgres and Redis on every request, so it represents a heavier per-request cost than the cached redirect.

| Path | Via nginx | Direct Go app |
|---|---|---|
| `GET /health` | 803 req/sec | 1,883 req/sec |

---

## What the Numbers Mean

- **36K req/sec** on the redirect path shows the impact of the Redis caching layer. Every cache hit avoids a Postgres round-trip entirely — only a single Redis GET and an async RPUSH for click tracking.
- The async click flush (goroutine draining `clicks:<id>` every 30 seconds) means zero synchronous DB writes on the hot path, which is why latency stays under 3ms even at high concurrency.
- The nginx overhead on a single machine is real but not representative of a tuned deployment. In production this stack would use `worker_processes auto`, upstream keepalive, and potentially multiple app instances behind the load balancer.
