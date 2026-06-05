# URL Shortener

A production-ready URL shortening service built with Go, PostgreSQL, and Redis.

**Live demo:** [http://127.0.0.1:80/health]

---

## Features

- Shorten any valid HTTP/HTTPS URL and get a short code back
- Redirect via short code with Redis caching for fast lookups
- Click analytics tracked per-timestamp and flushed asynchronously to Postgres
- JWT authentication — register, log in, manage only your own URLs
- URL expiry support via optional `expires_at` field
- Rate limiting per IP to prevent abuse
- Health endpoint reporting live status of all dependencies

---

## Tech Stack

| Layer | Technology | Why |
|---|---|---|
| Language | Go | [your reasoning] |
| Database | PostgreSQL | [your reasoning] |
| Cache | Redis | [your reasoning] |
| Auth | JWT | [your reasoning] |
| Reverse Proxy | nginx | [your reasoning] |
| Deployment | Docker Compose + Tailscale Funnel | [your reasoning] |

---

## Architecture

```
Client → Tailscale Funnel (TLS) → nginx → Go app → PostgreSQL
                                                   → Redis
```

**Redirect flow:**
1. Request hits nginx, forwarded to Go app
2. App checks Redis cache — if hit, redirect immediately (302)
3. On cache miss, query PostgreSQL, populate Redis, redirect (301)
4. Short code and timestamp pushed to a Redis list (`clicks:<id>`)
5. Background goroutine flushes click lists to Postgres every 30 seconds

**Why async click flushing?**
[Explain your tradeoff here — synchronous DB write on every redirect vs. Redis buffer]

---

## API

### Auth
| Method | Endpoint | Auth | Description |
|---|---|---|---|
| POST | `/register` | None | Create account, returns JWT |
| POST | `/login` | None | Login, returns JWT |

### URLs
| Method | Endpoint | Auth | Description |
|---|---|---|---|
| POST | `/urls` | Required | Shorten a URL |
| GET | `/urls` | Required | List your URLs |
| DELETE | `/urls/{shortCode}` | Required | Delete your URL |
| GET | `/{shortCode}` | None | Redirect to original URL |

### System
| Method | Endpoint | Auth | Description |
|---|---|---|---|
| GET | `/health` | None | Returns status of app, Postgres, and Redis |

**Example — create a short URL:**
```bash
curl -X POST https://your-domain/urls \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"long_url": "https://example.com", "expires_at": "2027-01-01T00:00:00Z"}'
```

---

## Running Locally

**Prerequisites:** Docker, Docker Compose

1. Clone the repo
2. Create a `.env` file:
   ```
   JWT_SECRET=your-secret-here
   ```
3. Start the stack:
   ```bash
   docker compose up --build
   ```
4. The app is available at `http://localhost:80`

---

## Running Tests

Pure function and auth tests (no dependencies needed):
```bash
go test -v -run "TestEncodeDecode|TestAuthSuite" ./...
```

Full test suite (requires Postgres):
```bash
TEST_POSTGRES_CONN="host=localhost user=myuser password=mypassword dbname=mydb sslmode=disable" \
  go test -v ./...
```

---

## Tradeoffs & Design Decisions

**[Fill this in — this is the most important section for interviews]**

A few prompts:
- Why Redis for caching instead of in-memory?
- Why async click flushing instead of writing to Postgres on each redirect?
- Why JWT over sessions?
- What would you change if this needed to handle 10x the traffic?
