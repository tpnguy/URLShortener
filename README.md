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
| Language | Go | [Go's goroutine model fits this project well — every redirect spawns a goroutine at around 2KB compared to a thread at around 1MB, so the service handles a lot of concurrent requests cheaply. The background Redis flush also runs as a goroutine without needing a separate thread pool. Go's standard library handles HTTP, JSON, and crypto without extra dependencies, and the single-binary build makes Docker deployment straightforward. I also don't have to worry about memory safety issues like I would in C or C++. On top of that, I wanted to deepen my backend skills in Go specifically, and I've found the syntax easy to read and the documentation easy to navigate.] |
| Database | PostgreSQL | [I wanted to store my data reliably and ensure functionality, and I heard that PostgreSQL was a popular choice. It's an object relational database that uses SQL, and apparently it has a feature that says that it's ACID compliant. Looking it up, it means that it's follows the principles of Atomicity, Consistency, Isolation, and Durability. While it might be somewhat overkill for this small project, it'll ensure that all transactions succeed, if violating a rule: cancelled, concurrency safe since they don't interact with each other, and permanant resulting. For a project like this, I used PostgreSQL since it ensures that all my operations are properly formatted and adhere to my rules; so even if something went wrong, it would void the transaction and maintain the integrity of my databases.] |
| Cache | Redis | [I chose Redis because of a previous project that I worked on, the Steam Zeroplay on my Github. That project made use of a backend such as this, but I found problems with the amount of users trying to access it. When there were too many requests being made at the same time, the whole backend was slow for all users since they had to process everything in a queue.  Researching how I could fix this, I landed on Redis. Redis to me is the universal database that all consumers talk to, and if it wasn't in the Redis cache, only then would it query the main database. Using Redis, I'm able to reduce the loading time by having a consumer query be put in the Redis, and if another user has the same kind of query they can refer to that there instead of the main database. This makes the whole process cheaper. Basically RAM vs CPU cache.] |
| Auth | JWT | [This kind of project relies a lot on APIs to work, so choosing something fast and reliable without DB lookup would be what I need for this project. I chose JWT since it is stateless, meaning that I can just use a token as truth, and be able to verify comparing this token. This is quick as I don't to make a call to the DB, which could be quite expensive compared to the type of service this project is trying to provide, which is a bunch of people trying to shorten their URLs. That kind of service should be instant. I also determined that the tradeoff for using this tech would be insignificant. The cons of using this means that the token is absolute, and I can't invalidate it until it expires. There isn't that much sensitive data in the payload for the token in this project either, making it a more appealing option as well.] |
| Reverse Proxy | nginx | [To be honest, I only thought of this tech at the end. I've already implemented my own version of rate limiting, and if I had known about nginx before I would've used their much more preferable architecture since it blocks requests even before it hits my application, reducing the amount of delay there is. The main reason I used this instead was because of the load balancing and terminating the TLS (Transport Layer Security) protocol. And because I didn't want to use my tailscale funnel IP which was kind of long, and have it route to an easier proxy. This kind of project could benefit from load balancing, as I'm sure that URL shortening will be used frequently, and this could distribute to other servers if one is being overwhelmed. Not only that, but the feature of HTTPS being encrypting and decrypting every request could be quite costly, but if I had a certificate in the future where I place it on nginx, then it'll all be validated easier. But I also guess that Tailscale Funnel does this already, so it doubles over.] |
| Deployment | Docker Compose + Tailscale Funnel | [I wanted my project to be easily built on any architecture. I've shared code with friends who own different operating systems, and it works on my PC but their PC it doesn't work. Docker eliminates this. I also wanted this project to be hosted on the internet without the traditional company like Fly.io running my things. This ensures I own the whole tech stack.] |

---

## Architecture

```
Client → Tailscale Funnel (TLS) → nginx → Go app → PostgreSQL → Redis
```

**Redirect flow:**
1. Request hits nginx, forwarded to Go app
2. App checks Redis cache — if hit, redirect immediately (302)
3. On cache miss, query PostgreSQL, populate Redis, redirect (301)
4. Short code and timestamp pushed to a Redis list (`clicks:<id>`)
5. Background goroutine flushes click lists to Postgres every 30 seconds

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

**Why async click flushing?**
Async click flushing exists in this project to ensure that the Redis buffer makes the user experience less delayed. A traditional synchronous DB write for PostgreSQL would require the insert function for a click have to go through a network round trip to the DB, acquiring a write lock, a WAL (write-ahead log) entry, and wait for acknowledgement. These subprocesses can take a long time for a heavy input/output based project like this since it blocks up queues. So for that reason, a Redis buffer is introduced to provide sub-milisecond in memory operation. It's seen by every process, allowing for quicker reads and writes. Every thirty seconds, it "flushes" this log of URLs and inserts into the DB, reducing load. One of the only cons that I can think of are that within this 30 seconds, if anything were to happen to the application -- be it crash or whatnot, we'll lose those 30 seconds of clicks. This isn't that bad for click analytics.

**Why JWT over sessions?**
The reason why JWT is used over sessions is because it's stateless and produces a token that should be used as truth for verification. This reduces the resources needed to make an operation, since it bypasses the round-trip to the DB. The tradeoff is that token cannot be changed when generated and sensitive payloads are prone to breaches, but this project uses a 30 minute timer for tokens and doesn't send sensitive information, making JWT an appealing choice.

**What would you change if this needed to handle 10x the traffic?**
I'd scale horizontally with nginx and mostly change the rate limiting to nginx as well since that's supported by people with more experience than me and is just overall better than a homemade version. Postgres connections are expensive, so I'd also implement some sort of connection pooling like pgbouncer to compile all of the connections together and reuse them. If there were a bunch of traffic, that might overwhelm the Postgres connection but with pgbouncer centralizing everything and distributing to the connections that are underutilized, it'll be able to handle load.