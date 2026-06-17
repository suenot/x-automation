// Package parser implements the w_popularity x (Twitter) adapter.
//
// History: an earlier revision scraped public Nitter mirrors. As of 2026
// every public Nitter mirror returns 5xx, Anubis challenges, or DNS failures
// from server environments, so that approach has been abandoned.
//
// Strategy (tried in order):
//
//  1. Official X API v2 (preferred). Requires Config.BearerToken
//     (or env X_BEARER_TOKEN). One GET /2/users/by/username/<handle> with
//     public_metrics gives Followers / PostsCount / TotalLikes. Free tier:
//     100 user reads / month / app — fine for daily snapshots.
//
//  2. cdn.syndication.twimg.com/timeline/profile (no auth). Twitter's
//     embed-syndication CDN. When it serves a body, the JSON wraps an
//     HTML page containing an `__INITIAL_STATE__` blob with the user
//     payload. The endpoint has been ratcheted down to empty bodies
//     since ~2024 but we still try it: when it works it is the cheapest
//     unauthenticated path.
//
//  3. api.fxtwitter.com (third-party proxy). Reliable JSON-only proxy
//     returning followers / following / tweets / likes / verification.
//     Owned by FixTweet; rate-limited but free.
//
//  4. camoufox-driven browser fallback. Not yet wired — `Config.CamoufoxURL`
//     is reserved for that path. When set we return an explicit
//     "not implemented" hint so the caller can decide whether to
//     escalate to a manual / staffed fallback.
package parser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	shared "github.com/suenot/socials-auto"
)

// DefaultUserAgent is sent on every unauthenticated request. Some endpoints
// (notably cdn.syndication.twimg.com) reject bare Go clients with empty
// bodies, so we mimic a recent desktop browser.
const DefaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

const (
	defaultAPIBaseURL     = "https://api.x.com"
	defaultSyndicationURL = "https://cdn.syndication.twimg.com"
	defaultFxTwitterURL   = "https://api.fxtwitter.com"
)

// Config controls runtime behaviour.
//
//   - BearerToken: X API v2 bearer (free-tier OAuth 2.0 app token). When
//     non-empty the API v2 path is tried first.
//   - HTTPClient: optional; constructed from HTTPTimeout otherwise.
//   - HTTPTimeout: per-request budget. Default: 15s.
//   - UserAgent: overrides DefaultUserAgent.
//   - CamoufoxURL: reserved for a future browser-driven fallback. Not yet
//     consumed by this implementation.
//   - APIBaseURL: override https://api.x.com (test hook).
//   - SyndicationURL: override https://cdn.syndication.twimg.com (test hook).
//   - FxTwitterURL: override https://api.fxtwitter.com (test hook).
//   - NitterMirrors: accepted for backward compatibility, IGNORED at runtime.
//     Public Nitter mirrors are no longer reliable; the field is kept so
//     existing configs do not fail to parse.
type Config struct {
	BearerToken    string
	HTTPClient     *http.Client
	HTTPTimeout    time.Duration
	UserAgent      string
	CamoufoxURL    string
	APIBaseURL     string
	SyndicationURL string
	FxTwitterURL   string

	// Deprecated. Accepted but ignored. Will be removed.
	NitterMirrors []string
}

// New constructs a parser. It does not touch the network at construction time.
func New(cfg Config) *XParser {
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 15 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent
	}
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = defaultAPIBaseURL
	}
	if cfg.SyndicationURL == "" {
		cfg.SyndicationURL = defaultSyndicationURL
	}
	if cfg.FxTwitterURL == "" {
		cfg.FxTwitterURL = defaultFxTwitterURL
	}
	cfg.APIBaseURL = strings.TrimRight(cfg.APIBaseURL, "/")
	cfg.SyndicationURL = strings.TrimRight(cfg.SyndicationURL, "/")
	cfg.FxTwitterURL = strings.TrimRight(cfg.FxTwitterURL, "/")
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: cfg.HTTPTimeout}
	}
	return &XParser{cfg: cfg}
}

// XParser is the X (Twitter) adapter.
type XParser struct{ cfg Config }

// Platform returns shared.PlatformX.
func (p *XParser) Platform() shared.Platform { return shared.PlatformX }

// FetchChannel tries each strategy in order until one returns a populated
// snapshot. See package doc for the priority list.
func (p *XParser) FetchChannel(ctx context.Context, handle string) (shared.ChannelSnapshot, error) {
	h := normaliseHandle(handle)
	if h == "" {
		return shared.ChannelSnapshot{}, fmt.Errorf("x: empty handle")
	}

	var errs []string

	// 1. Official API v2.
	if p.cfg.BearerToken != "" {
		snap, err := p.fetchChannelViaAPI(ctx, h)
		if err == nil {
			return snap, nil
		}
		// Definitive auth / not-found / rate errors short-circuit — they
		// will not be cured by trying the public paths.
		if errors.Is(err, shared.ErrNotFound) ||
			errors.Is(err, shared.ErrAuth) ||
			errors.Is(err, shared.ErrRateLimited) {
			return shared.ChannelSnapshot{}, err
		}
		errs = append(errs, "api: "+err.Error())
	}

	// 2. Syndication CDN.
	snap, err := p.fetchChannelViaSyndication(ctx, h)
	if err == nil {
		return snap, nil
	}
	if errors.Is(err, shared.ErrNotFound) {
		return shared.ChannelSnapshot{}, err
	}
	errs = append(errs, "syndication: "+err.Error())

	// 3. fxtwitter proxy.
	snap, err = p.fetchChannelViaFxTwitter(ctx, h)
	if err == nil {
		return snap, nil
	}
	if errors.Is(err, shared.ErrNotFound) {
		return shared.ChannelSnapshot{}, err
	}
	errs = append(errs, "fxtwitter: "+err.Error())

	// 4. Camoufox (stub).
	snap, err = p.fetchViaCamoufox(ctx, h)
	if err == nil {
		return snap, nil
	}
	errs = append(errs, "camoufox: "+err.Error())

	return shared.ChannelSnapshot{},
		fmt.Errorf("x: %w: all X scraping paths failed; configure X_BEARER_TOKEN or CamoufoxURL: %s",
			shared.ErrAuth, strings.Join(errs, "; "))
}

// FetchRecentPosts returns recent posts. Only the API v2 path returns
// engagement metrics; the public unauthenticated paths return URL + ID +
// PublishedAt and leave Likes/Views/Comments at zero (or just return empty
// when the path cannot reach the user timeline).
func (p *XParser) FetchRecentPosts(ctx context.Context, handle string, since time.Time) ([]shared.PostSnapshot, error) {
	h := normaliseHandle(handle)
	if h == "" {
		return nil, fmt.Errorf("x: empty handle")
	}

	// Only the API v2 path can reliably enumerate a user's tweets in
	// 2026. Anything else returns at best a single most-recent tweet.
	// When no bearer token is set we return an empty slice rather than
	// surfacing an error: callers downstream treat empty as "no posts
	// available".
	if p.cfg.BearerToken == "" {
		return nil, nil
	}

	return p.fetchPostsViaAPI(ctx, h, since)
}

// ---------------------------------------------------------------------------
// 1. X API v2
// ---------------------------------------------------------------------------

type apiUserResp struct {
	Data struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Username      string `json:"username"`
		Description   string `json:"description"`
		CreatedAt     string `json:"created_at"`
		Verified      bool   `json:"verified"`
		PublicMetrics struct {
			FollowersCount int64 `json:"followers_count"`
			FollowingCount int64 `json:"following_count"`
			TweetCount     int64 `json:"tweet_count"`
			ListedCount    int64 `json:"listed_count"`
			LikeCount      int64 `json:"like_count"`
		} `json:"public_metrics"`
	} `json:"data"`
	Errors []struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Type   string `json:"type"`
	} `json:"errors"`
}

func (p *XParser) fetchChannelViaAPI(ctx context.Context, handle string) (shared.ChannelSnapshot, error) {
	target := p.cfg.APIBaseURL + "/2/users/by/username/" + url.PathEscape(handle) +
		"?user.fields=public_metrics,name,description,created_at,verified"

	body, err := p.get(ctx, target, http.Header{
		"Authorization": []string{"Bearer " + p.cfg.BearerToken},
		"Accept":        []string{"application/json"},
	})
	if err != nil {
		return shared.ChannelSnapshot{}, err
	}

	var resp apiUserResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return shared.ChannelSnapshot{},
			fmt.Errorf("api decode: %w: %v", shared.ErrTransient, err)
	}
	if resp.Data.ID == "" {
		// API v2 returns 200 + an errors array for "not found".
		for _, e := range resp.Errors {
			if strings.Contains(strings.ToLower(e.Title), "not found") ||
				strings.Contains(strings.ToLower(e.Detail), "could not be found") {
				return shared.ChannelSnapshot{},
					fmt.Errorf("api: %w: %s", shared.ErrNotFound, e.Detail)
			}
		}
		return shared.ChannelSnapshot{},
			fmt.Errorf("api: %w: empty data, no errors", shared.ErrTransient)
	}

	raw := map[string]interface{}{
		"source":          "api_v2",
		"id":              resp.Data.ID,
		"name":            resp.Data.Name,
		"description":     resp.Data.Description,
		"created_at":      resp.Data.CreatedAt,
		"verified":        resp.Data.Verified,
		"listed_count":    resp.Data.PublicMetrics.ListedCount,
		"following_count": resp.Data.PublicMetrics.FollowingCount,
	}

	return shared.ChannelSnapshot{
		Platform:   shared.PlatformX,
		Handle:     handle,
		URL:        "https://x.com/" + handle,
		FetchedAt:  time.Now().UTC(),
		Followers:  resp.Data.PublicMetrics.FollowersCount,
		PostsCount: resp.Data.PublicMetrics.TweetCount,
		TotalLikes: resp.Data.PublicMetrics.LikeCount,
		Raw:        raw,
	}, nil
}

type apiTweetsResp struct {
	Data []struct {
		ID            string `json:"id"`
		Text          string `json:"text"`
		CreatedAt     string `json:"created_at"`
		PublicMetrics struct {
			RetweetCount int64 `json:"retweet_count"`
			ReplyCount   int64 `json:"reply_count"`
			LikeCount    int64 `json:"like_count"`
			QuoteCount   int64 `json:"quote_count"`
			BookmarkCount int64 `json:"bookmark_count"`
			ImpressionCount int64 `json:"impression_count"`
		} `json:"public_metrics"`
	} `json:"data"`
	Errors []struct {
		Title  string `json:"title"`
		Detail string `json:"detail"`
	} `json:"errors"`
}

func (p *XParser) fetchPostsViaAPI(ctx context.Context, handle string, since time.Time) ([]shared.PostSnapshot, error) {
	// We need the user id first. Reuse the channel call (cheap).
	snap, err := p.fetchChannelViaAPI(ctx, handle)
	if err != nil {
		return nil, err
	}
	userID, _ := snap.Raw["id"].(string)
	if userID == "" {
		return nil, fmt.Errorf("api: %w: missing user id", shared.ErrTransient)
	}

	q := url.Values{}
	q.Set("max_results", "50")
	q.Set("tweet.fields", "public_metrics,created_at")
	target := p.cfg.APIBaseURL + "/2/users/" + url.PathEscape(userID) +
		"/tweets?" + q.Encode()

	body, err := p.get(ctx, target, http.Header{
		"Authorization": []string{"Bearer " + p.cfg.BearerToken},
		"Accept":        []string{"application/json"},
	})
	if err != nil {
		return nil, err
	}
	var resp apiTweetsResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("api decode: %w: %v", shared.ErrTransient, err)
	}

	now := time.Now().UTC()
	out := make([]shared.PostSnapshot, 0, len(resp.Data))
	for _, t := range resp.Data {
		published, _ := time.Parse(time.RFC3339, t.CreatedAt)
		if !since.IsZero() && !published.IsZero() && published.Before(since) {
			continue
		}
		out = append(out, shared.PostSnapshot{
			Platform:      shared.PlatformX,
			ChannelHandle: handle,
			PostID:        t.ID,
			URL:           "https://x.com/" + handle + "/status/" + t.ID,
			Kind:          shared.PostKindPost,
			PublishedAt:   published.UTC(),
			FetchedAt:     now,
			Likes:         t.PublicMetrics.LikeCount,
			Views:         t.PublicMetrics.ImpressionCount,
			Comments:      t.PublicMetrics.ReplyCount,
			Shares:        t.PublicMetrics.RetweetCount + t.PublicMetrics.QuoteCount,
			Raw: map[string]interface{}{
				"source":         "api_v2",
				"text":           t.Text,
				"retweet_count":  t.PublicMetrics.RetweetCount,
				"quote_count":    t.PublicMetrics.QuoteCount,
				"bookmark_count": t.PublicMetrics.BookmarkCount,
			},
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 2. Syndication CDN
// ---------------------------------------------------------------------------

// syndicationEnvelope is the outer shape served by the embed-syndication CDN.
// The body field carries an HTML document with an __INITIAL_STATE__ JSON
// blob inlined inside a <script>.
type syndicationEnvelope struct {
	Body string `json:"body"`
}

// initialStateUser captures the fields we care about inside
// __INITIAL_STATE__.user (typed loosely because the schema is undocumented
// and changes occasionally).
type initialStateRoot struct {
	User struct {
		ScreenName     string `json:"screen_name"`
		IDStr          string `json:"id_str"`
		Name           string `json:"name"`
		Description    string `json:"description"`
		CreatedAt      string `json:"created_at"`
		Verified       bool   `json:"verified"`
		FollowersCount int64  `json:"followers_count"`
		FriendsCount   int64  `json:"friends_count"`
		StatusesCount  int64  `json:"statuses_count"`
		FavouritesCount int64 `json:"favourites_count"`
		ListedCount    int64  `json:"listed_count"`
	} `json:"user"`
}

var initialStateRe = regexp.MustCompile(`(?s)__INITIAL_STATE__\s*=\s*(\{.*?\})\s*;`)

func (p *XParser) fetchChannelViaSyndication(ctx context.Context, handle string) (shared.ChannelSnapshot, error) {
	q := url.Values{}
	q.Set("screen_name", handle)
	q.Set("suppress_response_codes", "true")
	q.Set("lang", "en")
	target := p.cfg.SyndicationURL + "/timeline/profile?" + q.Encode()

	body, err := p.get(ctx, target, http.Header{
		"Accept":   []string{"application/json, text/javascript, */*; q=0.01"},
		"Referer":  []string{"https://platform.twitter.com/"},
		"Origin":   []string{"https://platform.twitter.com"},
	})
	if err != nil {
		return shared.ChannelSnapshot{}, err
	}

	if len(strings.TrimSpace(string(body))) == 0 {
		return shared.ChannelSnapshot{},
			fmt.Errorf("syndication: %w: empty body (CDN gated)", shared.ErrTransient)
	}

	var env syndicationEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return shared.ChannelSnapshot{},
			fmt.Errorf("syndication: %w: %v", shared.ErrTransient, err)
	}
	if env.Body == "" {
		return shared.ChannelSnapshot{},
			fmt.Errorf("syndication: %w: missing body field", shared.ErrTransient)
	}

	m := initialStateRe.FindStringSubmatch(env.Body)
	if len(m) < 2 {
		return shared.ChannelSnapshot{},
			fmt.Errorf("syndication: %w: no __INITIAL_STATE__ blob", shared.ErrTransient)
	}
	var state initialStateRoot
	if err := json.Unmarshal([]byte(m[1]), &state); err != nil {
		return shared.ChannelSnapshot{},
			fmt.Errorf("syndication: %w: parse state: %v", shared.ErrTransient, err)
	}
	if state.User.FollowersCount == 0 && state.User.StatusesCount == 0 {
		return shared.ChannelSnapshot{},
			fmt.Errorf("syndication: %w: state missing counters", shared.ErrTransient)
	}

	raw := map[string]interface{}{
		"source":          "syndication",
		"id":              state.User.IDStr,
		"name":            state.User.Name,
		"description":     state.User.Description,
		"created_at":      state.User.CreatedAt,
		"verified":        state.User.Verified,
		"listed_count":    state.User.ListedCount,
		"following_count": state.User.FriendsCount,
	}
	return shared.ChannelSnapshot{
		Platform:   shared.PlatformX,
		Handle:     handle,
		URL:        "https://x.com/" + handle,
		FetchedAt:  time.Now().UTC(),
		Followers:  state.User.FollowersCount,
		PostsCount: state.User.StatusesCount,
		TotalLikes: state.User.FavouritesCount,
		Raw:        raw,
	}, nil
}

// ---------------------------------------------------------------------------
// 3. fxtwitter proxy
// ---------------------------------------------------------------------------

type fxTwitterResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	User    struct {
		ScreenName  string `json:"screen_name"`
		URL         string `json:"url"`
		ID          string `json:"id"`
		Followers   int64  `json:"followers"`
		Following   int64  `json:"following"`
		Likes       int64  `json:"likes"`
		MediaCount  int64  `json:"media_count"`
		Tweets      int64  `json:"tweets"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Joined      string `json:"joined"`
		Protected   bool   `json:"protected"`
		Verification struct {
			Verified bool   `json:"verified"`
			Type     string `json:"type"`
		} `json:"verification"`
	} `json:"user"`
}

func (p *XParser) fetchChannelViaFxTwitter(ctx context.Context, handle string) (shared.ChannelSnapshot, error) {
	target := p.cfg.FxTwitterURL + "/" + url.PathEscape(handle)
	body, err := p.get(ctx, target, http.Header{
		"Accept": []string{"application/json"},
	})
	if err != nil {
		return shared.ChannelSnapshot{}, err
	}
	var resp fxTwitterResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return shared.ChannelSnapshot{},
			fmt.Errorf("fxtwitter: %w: %v", shared.ErrTransient, err)
	}
	if resp.Code == http.StatusNotFound {
		return shared.ChannelSnapshot{},
			fmt.Errorf("fxtwitter: %w: %s", shared.ErrNotFound, resp.Message)
	}
	if resp.User.ScreenName == "" && resp.User.ID == "" {
		return shared.ChannelSnapshot{},
			fmt.Errorf("fxtwitter: %w: empty user (code=%d msg=%q)",
				shared.ErrTransient, resp.Code, resp.Message)
	}

	raw := map[string]interface{}{
		"source":          "fxtwitter",
		"id":              resp.User.ID,
		"name":            resp.User.Name,
		"description":     resp.User.Description,
		"joined":          resp.User.Joined,
		"verified":        resp.User.Verification.Verified,
		"following_count": resp.User.Following,
		"media_count":     resp.User.MediaCount,
	}
	return shared.ChannelSnapshot{
		Platform:   shared.PlatformX,
		Handle:     handle,
		URL:        "https://x.com/" + handle,
		FetchedAt:  time.Now().UTC(),
		Followers:  resp.User.Followers,
		PostsCount: resp.User.Tweets,
		TotalLikes: resp.User.Likes,
		Raw:        raw,
	}, nil
}

// ---------------------------------------------------------------------------
// 4. Camoufox fallback (stub)
// ---------------------------------------------------------------------------

func (p *XParser) fetchViaCamoufox(_ context.Context, _ string) (shared.ChannelSnapshot, error) {
	if p.cfg.CamoufoxURL == "" {
		return shared.ChannelSnapshot{},
			fmt.Errorf("%w: camoufox not configured", shared.ErrNotImplemented)
	}
	return shared.ChannelSnapshot{},
		fmt.Errorf("%w: camoufox CDP path not yet wired (CamoufoxURL=%q)",
			shared.ErrNotImplemented, p.cfg.CamoufoxURL)
}

// ---------------------------------------------------------------------------
// HTTP plumbing
// ---------------------------------------------------------------------------

func (p *XParser) get(ctx context.Context, target string, extra http.Header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", p.cfg.UserAgent)
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	for k, vs := range extra {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}

	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", shared.ErrTransient, err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)

	switch {
	case resp.StatusCode == http.StatusOK:
		// proceed
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("http 404: %w", shared.ErrNotFound)
	case resp.StatusCode == http.StatusUnauthorized,
		resp.StatusCode == http.StatusForbidden:
		// Auth-bearing endpoints only — treat as terminal.
		hasAuth := req.Header.Get("Authorization") != ""
		if hasAuth {
			return nil, fmt.Errorf("http %d: %w", resp.StatusCode, shared.ErrAuth)
		}
		return nil, fmt.Errorf("http %d: %w", resp.StatusCode, shared.ErrTransient)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("http 429: %w", shared.ErrRateLimited)
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("http %d: %w", resp.StatusCode, shared.ErrTransient)
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("http %d: %w", resp.StatusCode, shared.ErrTransient)
	}

	if readErr != nil {
		return nil, fmt.Errorf("%w: read body: %v", shared.ErrTransient, readErr)
	}
	return body, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func normaliseHandle(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "@")
	return strings.ToLower(h)
}

// Compile-time interface check.
var _ shared.Parser = (*XParser)(nil)
