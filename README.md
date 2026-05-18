# w-popularity-parser-x

X (Twitter) parser for [w_popularity](https://github.com/suenot/w-popularity).

## Strategy

- **Primary:** [Nitter](https://github.com/zedeus/nitter) mirrors — HTML for
  channel snapshots (followers / tweet count / total likes / display name /
  bio) and RSS for the user's most recent posts.
- **Fallback:** camoufox-driven browser scraping. **Not yet wired** — `Config.CamoufoxURL`
  is reserved for this path. When every Nitter mirror fails the parser returns
  `shared.ErrTransient` with a hint to invoke the camoufox fallback.

The official `nitter.net` is no longer reliable, and public mirrors come and
go. The parser tries each configured mirror in order and tolerates outages
by failing over to the next. Operators **should expect** the default list to
go stale and override `Config.NitterMirrors` from configuration / env.

### What this parser does and does not collect

| Field                        | Source                  |
|------------------------------|-------------------------|
| `ChannelSnapshot.Followers`  | Nitter HTML stat block  |
| `ChannelSnapshot.PostsCount` | Nitter HTML stat block  |
| `ChannelSnapshot.TotalLikes` | Nitter HTML stat block  |
| `ChannelSnapshot.Raw[display_name/bio/mirror/source]` | Nitter HTML |
| `PostSnapshot.PostID/URL/PublishedAt/Title` | Nitter RSS |
| `PostSnapshot.Likes/Views/Comments` | **always 0** — RSS does not expose engagement counts, and scraping each post page individually is intentionally skipped (too expensive). |

## Usage

```go
import parser "github.com/suenot/w-popularity-parser-x"

p := parser.New(parser.Config{
    // Override defaults — public mirror availability changes weekly.
    NitterMirrors: strings.Split(os.Getenv("NITTER_MIRRORS"), ","),
    HTTPTimeout:   10 * time.Second,
})

snap, err := p.FetchChannel(ctx, "elonmusk")
posts, err := p.FetchRecentPosts(ctx, "elonmusk", time.Now().Add(-24*time.Hour))
```

If `Config.NitterMirrors` is empty, `parser.DefaultNitterMirrors` is used.

### Default mirror list (at time of writing)

```text
https://xcancel.com
https://nitter.privacydev.net
https://nitter.poast.org
https://nitter.kavin.rocks
https://nitter.net
```

These addresses go stale frequently. Refer to community-maintained
instance lists (e.g. https://github.com/zedeus/nitter/wiki/Instances) and
configure `NITTER_MIRRORS` accordingly.

## Errors

- `shared.ErrNotFound` — mirror returned 404 *and* page text contains a
  Nitter-style "User \"x\" not found" marker. Short-circuits the mirror loop.
- `shared.ErrRateLimited` — any mirror returned HTTP 429.
- `shared.ErrTransient` — every mirror failed, returned non-2xx, returned
  HTML that does not contain a `profile-stat-num` block, or returned an RSS
  payload that does not parse as XML. Operators may then invoke the camoufox
  fallback (TODO).

## License

MIT
