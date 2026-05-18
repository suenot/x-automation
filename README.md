# w-popularity-parser-x

`x` parser for [w_popularity](https://github.com/suenot/w-popularity).

**Status:** stub. `FetchChannel` and `FetchRecentPosts` return `shared.ErrNotImplemented`.

## Strategy

- **Primary:** X API v2 / Nitter mirrors
- **Fallback:** camoufox

## Usage

```go
import parser "github.com/suenot/w-popularity-parser-x"

p := parser.New(parser.Config{Credential: os.Getenv("CRED")})
snap, err := p.FetchChannel(ctx, handle)
```

## License

MIT
