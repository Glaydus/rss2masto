# rss2masto

[![Go Report Card](https://goreportcard.com/badge/github.com/glaydus/rss2masto)](https://goreportcard.com/report/github.com/glaydus/rss2masto)
[![Release](https://img.shields.io/github/v/release/glaydus/rss2masto)](https://github.com/glaydus/rss2masto/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/glaydus/rss2masto.svg)](https://pkg.go.dev/github.com/glaydus/rss2masto)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/glaydus/rss2masto)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](https://opensource.org/licenses/MIT)

A Go library for publishing RSS/Atom feed items as Mastodon posts. Designed to handle hundreds or thousands of feeds concurrently, with built-in scheduling, deduplication via Redis, and ETag-based conditional fetching.

## Features

- Concurrent processing of any number of RSS/Atom feeds using goroutines
- Per-feed scheduler with configurable check interval
- Deduplication via Redis — each item is posted exactly once
- ETag / If-None-Match support — unchanged feeds skip parsing entirely
- HTML sanitization and automatic post truncation to instance character limit
- Hashtag generation from feed item categories or URL patterns
- Text replacement rules per feed
- Post visibility control (public, unlisted, private)
- Automatic language detection from feed metadata
- Follower count tracking per Mastodon account
- Optional state persistence to `feed.yaml`

## Requirements

- Go 1.22+
- Redis (used for deduplication and caching)

## Installation

```sh
go get -u github.com/glaydus/rss2masto
```

Or add the import and run `go mod tidy`:

```go
import "github.com/glaydus/rss2masto"
```

## Quick start

```go
package main

import (
    "log"
    "time"

    "github.com/glaydus/rss2masto"
)

func main() {
    fm, err := rss2masto.NewFeedsMonitor()
    if err != nil {
        log.Fatalln(err)
    }

    // Run once
    fm.Start()

    // Or run on a ticker (e.g. every minute)
    ticker := time.NewTicker(time.Minute)
    for range ticker.C {
        fm.Start()
    }
}
```

`NewFeedsMonitor` reads `feed.yaml` from the current directory. Each call to `Start` processes all feeds whose scheduler counter has reached its configured interval.

## Configuration — feed.yaml

```yaml
instance:
  url: https://mastodon.example        # Mastodon instance base URL (HTTPS required)
  lang: en                             # default post language (ISO 639-1)
  timezone: Europe/Warsaw              # timezone for timestamps (IANA format)
  limit:                               # max characters per post; auto-detected from instance if empty
  save: false                          # persist last_run timestamps back to feed.yaml after each run

  feed:
    - name: My Tech Blog               # display name (used as log prefix and idempotency key prefix)
      url: https://example.com/rss     # RSS or Atom feed URL
      token: <MASTODON_API_TOKEN>      # Mastodon access token for this account
      interval: 10                     # check every N scheduler ticks (e.g. 10 = every 10 minutes if ticker is 1 min)
      visibility: public               # public | unlisted | private
      prefix: Tech                     # optional hashtag prefix added to every generated tag
      hashlink:                        # regex with one capture group — extracts hashtag from item URL
      replace_from:                    # regex applied to post description
      replace_to:                      # replacement string (used with replace_from)

    - name: Another Feed
      url: https://another.example/feed.xml
      token: <ANOTHER_TOKEN>
      interval: 30
      visibility: unlisted
```

### Field reference

| Field | Required | Default | Description |
|---|---|---|---|
| `instance.url` | yes | — | Mastodon instance base URL |
| `instance.lang` | no | `en` | Fallback post language |
| `instance.timezone` | no | `UTC` | Timezone for display timestamps |
| `instance.limit` | no | auto | Max post characters; fetched from instance API if not set |
| `instance.save` | no | `false` | Write updated `last_run` values back to `feed.yaml` |
| `feed.name` | no | derived from URL host | Feed identifier used in logs and idempotency keys |
| `feed.url` | yes | — | RSS/Atom feed endpoint |
| `feed.token` | yes | — | Mastodon API access token |
| `feed.interval` | no | `10` | Scheduler ticks between checks |
| `feed.visibility` | no | `private` | Mastodon post visibility |
| `feed.prefix` | no | — | Prefix added to each generated hashtag |
| `feed.hashlink` | no | — | Regex to extract a hashtag from the item link |
| `feed.replace_from` | no | — | Regex pattern applied to post description |
| `feed.replace_to` | no | — | Replacement string for `replace_from` matches |

## Redis

Redis is used for two purposes:

1. **Deduplication** — an idempotency key (`<feed_prefix>:<item_hash>`) is stored after each successful post. Items already in Redis are skipped on subsequent runs.
2. **Caching** — a local TinyLFU cache (backed by go-redis/cache) reduces Redis round-trips for hot keys.

The Redis connection is configured via the `REDIS_HOST` environment variable:

```sh
export REDIS_HOST=localhost:6379
```

The value is passed directly to `redis.ParseURL`, so full Redis URLs are also accepted:

```sh
export REDIS_HOST=redis://:password@localhost:6379/0
```

## Scheduler

`Start()` is designed to be called repeatedly on a fixed ticker. Each feed has an `interval` field that acts as a divisor — a feed with `interval: 10` is only processed on every 10th call to `Start()`. This lets you run a single tight ticker (e.g. every minute) while checking different feeds at different frequencies without managing multiple goroutines externally.

```
ticker: 1 minute
feed A interval: 5  → checked every  5 minutes
feed B interval: 15 → checked every 15 minutes
feed C interval: 60 → checked every  1 hour
```

`Start()` is safe to call concurrently — a built-in atomic guard prevents overlapping runs.

## Scaling

The library is designed to handle large numbers of feeds efficiently:

- All feeds within a single `Start()` call are processed in parallel via goroutines.
- HTTP fetching uses [fasthttp](https://github.com/valyala/fasthttp) with connection pooling and DNS caching.
- ETag support means unchanged feeds generate zero parsing overhead.
- Redis connection pool is pre-configured for high concurrency (20 connections, 5 idle minimum).

There is no hard limit on the number of feeds. Practical limits depend on available Redis connections, network bandwidth, and Mastodon API rate limits per token.

## Environment variables

| Variable | Description |
|---|---|
| `REDIS_HOST` | Redis address or URL (required in production) |

## License

MIT
