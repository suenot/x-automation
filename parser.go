// Package parser implements the w_popularity x (Twitter) adapter.
//
// Strategy:
//
//	primary:  Nitter mirrors (HTML for channel snapshots, RSS for recent posts).
//	fallback: camoufox-driven browser (TODO — not yet wired).
//
// Nitter is a privacy-focused Twitter front-end that exposes the same data as
// plain HTML and RSS without authentication. The original nitter.net is no
// longer reliable, and public mirrors come and go. The parser keeps a small
// configurable mirror list and tries each in turn on every call; if all of
// them fail it returns shared.ErrTransient so the caller can decide whether
// to invoke a heavier browser-based fallback.
package parser

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	shared "github.com/suenot/w-popularity-shared"
	"golang.org/x/net/html"
)

// DefaultNitterMirrors is a best-effort list of public Nitter instances that
// were responsive at the time of writing. Public mirrors disappear regularly,
// so operators should override Config.NitterMirrors from configuration / env
// when this list goes stale.
var DefaultNitterMirrors = []string{
	"https://xcancel.com",
	"https://nitter.privacydev.net",
	"https://nitter.poast.org",
	"https://nitter.kavin.rocks",
	"https://nitter.net",
}

// DefaultUserAgent is sent on every request. Some Nitter mirrors reject bare
// Go clients with a 403, so we mimic a recent desktop browser.
const DefaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// Config controls runtime behaviour.
//
//   - NitterMirrors: ordered list of base URLs to try (no trailing slash).
//     When empty, DefaultNitterMirrors is used.
//   - HTTPTimeout: per-mirror request budget. Defaults to 10s.
//   - UserAgent: overrides DefaultUserAgent.
//   - HTTPClient: optional; constructed from HTTPTimeout otherwise.
//   - CamoufoxURL: reserved for a future browser-driven fallback. Not yet
//     consumed by this implementation. TODO: wire camoufox-based scraping
//     when every Nitter mirror is exhausted.
//   - Credential: reserved (e.g. X API v2 bearer) for future paths. Unused.
type Config struct {
	NitterMirrors []string
	HTTPTimeout   time.Duration
	UserAgent     string
	HTTPClient    *http.Client
	CamoufoxURL   string // TODO: camoufox fallback
	Credential    string // TODO: optional X API path
}

// New constructs a parser. It does not touch the network at construction time.
func New(cfg Config) *XParser {
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 10 * time.Second
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = DefaultUserAgent
	}
	if len(cfg.NitterMirrors) == 0 {
		cfg.NitterMirrors = append([]string(nil), DefaultNitterMirrors...)
	}
	// Strip trailing slashes to keep URL concatenation predictable.
	for i, m := range cfg.NitterMirrors {
		cfg.NitterMirrors[i] = strings.TrimRight(m, "/")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: cfg.HTTPTimeout}
	}
	return &XParser{cfg: cfg}
}

// XParser is the X (Twitter) adapter.
type XParser struct{ cfg Config }

// Platform returns shared.PlatformX.
func (p *XParser) Platform() shared.Platform { return shared.PlatformX }

// FetchChannel hits each configured Nitter mirror in order until one returns
// a parseable profile HTML page. The handle may include a leading '@'.
func (p *XParser) FetchChannel(ctx context.Context, handle string) (shared.ChannelSnapshot, error) {
	h := normaliseHandle(handle)
	if h == "" {
		return shared.ChannelSnapshot{}, fmt.Errorf("x: empty handle")
	}

	var lastErr error
	for _, mirror := range p.cfg.NitterMirrors {
		snap, err := p.fetchChannelOnce(ctx, mirror, h)
		if err == nil {
			return snap, nil
		}
		// A definitive "handle does not exist" should not be masked by
		// failing over to another mirror — but Nitter mirrors love to
		// return 404 when *they* are misconfigured, so we only honour
		// ErrNotFound if it's confirmed by the mirror returning a real
		// "User \"x\" not found" page (mapped below). Other transient
		// errors fall through to the next mirror.
		if errors.Is(err, shared.ErrNotFound) {
			return shared.ChannelSnapshot{}, err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no mirrors configured")
	}
	return shared.ChannelSnapshot{},
		fmt.Errorf("x: %w: all Nitter mirrors failed; consider camoufox fallback (TODO): %v",
			shared.ErrTransient, lastErr)
}

// FetchRecentPosts pulls the user's Nitter RSS feed. Engagement counts are not
// included in RSS — Likes/Views/Comments stay at zero. Scraping each post page
// individually for those metrics is intentionally skipped (too expensive).
func (p *XParser) FetchRecentPosts(ctx context.Context, handle string, since time.Time) ([]shared.PostSnapshot, error) {
	h := normaliseHandle(handle)
	if h == "" {
		return nil, fmt.Errorf("x: empty handle")
	}

	var lastErr error
	for _, mirror := range p.cfg.NitterMirrors {
		posts, err := p.fetchRecentPostsOnce(ctx, mirror, h, since)
		if err == nil {
			return posts, nil
		}
		if errors.Is(err, shared.ErrNotFound) {
			return nil, err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("no mirrors configured")
	}
	return nil, fmt.Errorf("x: %w: all Nitter mirrors failed; consider camoufox fallback (TODO): %v",
		shared.ErrTransient, lastErr)
}

// --- Channel scrape ---------------------------------------------------------

func (p *XParser) fetchChannelOnce(ctx context.Context, mirror, handle string) (shared.ChannelSnapshot, error) {
	target := mirror + "/" + handle
	body, err := p.get(ctx, target)
	if err != nil {
		return shared.ChannelSnapshot{}, err
	}

	stats, displayName, bio, err := parseProfileHTML(body)
	if err != nil {
		return shared.ChannelSnapshot{}, fmt.Errorf("x %s: %w", mirror, err)
	}

	raw := map[string]interface{}{
		"source": "nitter",
		"mirror": mirror,
	}
	if displayName != "" {
		raw["display_name"] = displayName
	}
	if bio != "" {
		raw["bio"] = bio
	}

	return shared.ChannelSnapshot{
		Platform:   shared.PlatformX,
		Handle:     handle,
		URL:        "https://x.com/" + handle,
		FetchedAt:  time.Now().UTC(),
		Followers:  stats["followers"],
		PostsCount: stats["tweets"],
		TotalLikes: stats["likes"],
		Raw:        raw,
	}, nil
}

// parseProfileHTML walks the Nitter profile page and extracts the four stat
// numbers plus profile display name and bio. It returns ErrNotFound if the
// page looks like a "User not found" Nitter error page and a generic error
// if no stat blocks were found at all.
func parseProfileHTML(body []byte) (map[string]int64, string, string, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, "", "", fmt.Errorf("parse html: %v: %w", err, shared.ErrTransient)
	}

	// Detect Nitter's own "User \"x\" not found" page.
	if findText(doc, "User \"") || findText(doc, "User not found") {
		return nil, "", "", shared.ErrNotFound
	}

	stats := map[string]int64{}
	var displayName, bio string

	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			class := attr(n, "class")
			switch {
			case n.Data == "li" && strings.Contains(class, "posts"),
				n.Data == "li" && strings.Contains(class, "following"),
				n.Data == "li" && strings.Contains(class, "followers"),
				n.Data == "li" && strings.Contains(class, "likes"):
				header, num := extractStatPair(n)
				if header != "" && num != "" {
					if v, ok := parseNitterNumber(num); ok {
						stats[strings.ToLower(strings.TrimSpace(header))] = v
					}
				}
			case n.Data == "a" && strings.Contains(class, "profile-card-fullname"):
				if displayName == "" {
					displayName = strings.TrimSpace(innerText(n))
				}
			case n.Data == "div" && strings.Contains(class, "profile-bio"):
				if bio == "" {
					bio = strings.TrimSpace(innerText(n))
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if len(stats) == 0 {
		return nil, "", "", fmt.Errorf("no profile-stat blocks: %w", shared.ErrTransient)
	}

	// Some Nitter forks use "tweet" / "post" interchangeably — alias them.
	if v, ok := stats["tweet"]; ok && stats["tweets"] == 0 {
		stats["tweets"] = v
	}
	if v, ok := stats["post"]; ok && stats["tweets"] == 0 {
		stats["tweets"] = v
	}
	if v, ok := stats["posts"]; ok && stats["tweets"] == 0 {
		stats["tweets"] = v
	}

	return stats, displayName, bio, nil
}

// extractStatPair finds the profile-stat-header / profile-stat-num pair below
// a single <li> element. Returns (header, num) trimmed text or ("", "") when
// either is missing.
func extractStatPair(li *html.Node) (string, string) {
	var header, num string
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			class := attr(n, "class")
			if strings.Contains(class, "profile-stat-header") && header == "" {
				header = innerText(n)
			} else if strings.Contains(class, "profile-stat-num") && num == "" {
				num = innerText(n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(li)
	return strings.TrimSpace(header), strings.TrimSpace(num)
}

var nonDigit = regexp.MustCompile(`[^0-9KMB.kmb]`)

// parseNitterNumber accepts strings like "1,234", "12.3K", "1.2M", "239,913,818".
func parseNitterNumber(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	s = strings.ReplaceAll(s, ",", "")
	// Some mirrors localise with thin spaces or NBSPs.
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, " ", "")
	s = nonDigit.ReplaceAllString(s, "")
	if s == "" {
		return 0, false
	}

	var mul float64 = 1
	switch last := s[len(s)-1]; last {
	case 'K', 'k':
		mul = 1_000
		s = s[:len(s)-1]
	case 'M', 'm':
		mul = 1_000_000
		s = s[:len(s)-1]
	case 'B', 'b':
		mul = 1_000_000_000
		s = s[:len(s)-1]
	}
	if s == "" {
		return 0, false
	}
	if mul != 1 {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return int64(f * mul), true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// --- Recent posts (RSS) -----------------------------------------------------

type rssFeed struct {
	XMLName xml.Name `xml:"rss"`
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	PubDate string `xml:"pubDate"`
	GUID    string `xml:"guid"`
}

func (p *XParser) fetchRecentPostsOnce(ctx context.Context, mirror, handle string, since time.Time) ([]shared.PostSnapshot, error) {
	target := mirror + "/" + handle + "/rss"
	body, err := p.get(ctx, target)
	if err != nil {
		return nil, err
	}

	// Cheap shape sniff before invoking the XML parser: many "challenge"
	// pages we'd otherwise misinterpret as empty feeds start with HTML.
	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "<?xml") && !strings.HasPrefix(trimmed, "<rss") {
		return nil, fmt.Errorf("x %s rss: %w: non-XML response", mirror, shared.ErrTransient)
	}

	var feed rssFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("x %s rss: %w: %v", mirror, shared.ErrTransient, err)
	}

	now := time.Now().UTC()
	out := make([]shared.PostSnapshot, 0, len(feed.Channel.Items))
	for _, it := range feed.Channel.Items {
		published := parsePubDate(it.PubDate)
		if !since.IsZero() && !published.IsZero() && published.Before(since) {
			continue
		}
		postID := extractTweetID(it.Link)
		if postID == "" {
			postID = it.GUID
		}
		raw := map[string]interface{}{
			"source":  "nitter-rss",
			"mirror":  mirror,
			"title":   it.Title,
			"nitter":  it.Link,
		}
		// Normalise the canonical URL to twitter.com when Nitter emitted
		// its own mirror URL.
		canonical := canonicalTweetURL(handle, postID, it.Link)
		out = append(out, shared.PostSnapshot{
			Platform:      shared.PlatformX,
			ChannelHandle: handle,
			PostID:        postID,
			URL:           canonical,
			Kind:          shared.PostKindPost,
			PublishedAt:   published,
			FetchedAt:     now,
			Raw:           raw,
		})
	}
	return out, nil
}

func parsePubDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC1123Z,
		time.RFC1123,
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05 MST",
		time.RFC822Z,
		time.RFC822,
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

var tweetIDRe = regexp.MustCompile(`/status(?:es)?/(\d+)`)

func extractTweetID(link string) string {
	m := tweetIDRe.FindStringSubmatch(link)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

func canonicalTweetURL(handle, id, fallback string) string {
	if id != "" {
		return "https://x.com/" + handle + "/status/" + id
	}
	// Rewrite the host of the Nitter mirror URL to x.com when possible.
	if u, err := url.Parse(fallback); err == nil && u.Host != "" {
		u.Scheme = "https"
		u.Host = "x.com"
		return u.String()
	}
	return fallback
}

// --- HTTP plumbing ----------------------------------------------------------

func (p *XParser) get(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", p.cfg.UserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", shared.ErrTransient, err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK:
		// proceed
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("http 404: %w", shared.ErrNotFound)
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("http 429: %w", shared.ErrRateLimited)
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("http %d: %w", resp.StatusCode, shared.ErrTransient)
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("http %d: %w", resp.StatusCode, shared.ErrTransient)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", shared.ErrTransient, err)
	}
	return body, nil
}

// --- helpers ----------------------------------------------------------------

func normaliseHandle(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "@")
	return strings.ToLower(h)
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func innerText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func findText(n *html.Node, needle string) bool {
	if n.Type == html.TextNode && strings.Contains(n.Data, needle) {
		return true
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if findText(c, needle) {
			return true
		}
	}
	return false
}

// Compile-time interface check.
var _ shared.Parser = (*XParser)(nil)
