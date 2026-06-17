# x-auto

X (Twitter) parser for [w_popularity](https://github.com/suenot/w-popularity).

## Strategy

The earlier Nitter-based revision is gone: every public Nitter mirror was
returning 5xx, Anubis challenges, or DNS failures from server environments
in 2026. The parser now tries four paths in order:

1. **Official X API v2** — `GET /2/users/by/username/<handle>`
   with a free-tier bearer token (`Config.BearerToken` / `X_BEARER_TOKEN`).
   Returns `public_metrics` directly. Free tier: 100 user reads/month/app,
   plenty for daily snapshots. Also the only path that can enumerate a
   user's recent tweets with engagement metrics.

2. **cdn.syndication.twimg.com/timeline/profile** — Twitter's
   embed-syndication CDN, no auth required. The JSON envelope carries an
   HTML body containing an inline `__INITIAL_STATE__` blob with the user
   payload. This endpoint has been gated down to empty bodies for most
   unauthenticated callers since ~2024, but we still try it: when it
   works it is the cheapest path.

3. **api.fxtwitter.com** — third-party JSON proxy run by the FixTweet
   project. Reliable plain-JSON profile reads:
   `followers / following / tweets / likes / verification`. Rate-limited
   but free. This is currently the workhorse for the no-auth case.

4. **camoufox** — `Config.CamoufoxURL` is reserved for a future browser-
   driven fallback. Returns `shared.ErrNotImplemented` until wired.

When every path fails the parser returns `shared.ErrAuth` wrapped with
the hint `"all X scraping paths failed; configure X_BEARER_TOKEN or
CamoufoxURL"`.

### What this parser collects

| Field                                  | Source                                |
|----------------------------------------|---------------------------------------|
| `ChannelSnapshot.Followers`            | API v2 / syndication / fxtwitter      |
| `ChannelSnapshot.PostsCount`           | API v2 / syndication / fxtwitter      |
| `ChannelSnapshot.TotalLikes`           | API v2 / syndication / fxtwitter      |
| `ChannelSnapshot.Raw[name/description/created_at/verified/listed_count/following_count]` | All paths populate as available; `source` records which path won. |
| `PostSnapshot.Likes/Views/Comments/Shares` | API v2 only. Public paths return no posts. |

## Usage

```go
import parser "github.com/suenot/x-auto"

p := parser.New(parser.Config{
    BearerToken: os.Getenv("X_BEARER_TOKEN"), // optional
    HTTPTimeout: 15 * time.Second,
})

snap, err := p.FetchChannel(ctx, "elonmusk")
posts, err := p.FetchRecentPosts(ctx, "elonmusk", time.Now().Add(-24*time.Hour))
```

Without a bearer token, `FetchRecentPosts` returns an empty slice — none of
the unauthenticated paths can reliably enumerate a user's timeline with
engagement metrics. Use the bearer-token path or the camoufox fallback for
post-level data.

## Config

```go
type Config struct {
    BearerToken    string          // X API v2; env: X_BEARER_TOKEN
    HTTPClient     *http.Client
    HTTPTimeout    time.Duration   // default 15s
    UserAgent      string
    CamoufoxURL    string          // reserved for future fallback
    APIBaseURL     string          // override https://api.x.com (tests)
    SyndicationURL string          // override https://cdn.syndication.twimg.com (tests)
    FxTwitterURL   string          // override https://api.fxtwitter.com (tests)

    NitterMirrors  []string        // accepted for backward compat, IGNORED
}
```

## Errors

- `shared.ErrNotFound` — API v2 errors-array marks the handle as missing,
  or any path returns HTTP 404.
- `shared.ErrRateLimited` — any path returns HTTP 429.
- `shared.ErrAuth` — API v2 returns 401/403, OR every public path failed
  (with the configure-X_BEARER_TOKEN-or-CamoufoxURL hint).
- `shared.ErrTransient` — single-path transient errors (5xx, partial
  responses) that the parser silently retried against the next path.
- `shared.ErrNotImplemented` — camoufox path not yet wired.

## License

MIT
