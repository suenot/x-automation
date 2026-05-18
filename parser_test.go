package parser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	shared "github.com/suenot/w-popularity-shared"
)

// rewriteRoundTripper sends every outbound request to a single httptest base
// URL, ignoring the original scheme+host. The original "mirror" host is
// propagated via the X-Mirror header so the handler can vary its response
// per logical mirror.
type rewriteRoundTripper struct {
	base *url.URL
}

func (r *rewriteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.Header.Set("X-Mirror", req.URL.Host)
	out.URL = &url.URL{
		Scheme:   r.base.Scheme,
		Host:     r.base.Host,
		Path:     req.URL.Path,
		RawQuery: req.URL.RawQuery,
	}
	out.Host = r.base.Host
	return http.DefaultTransport.RoundTrip(out)
}

func newTestClient(serverURL string) *http.Client {
	base, _ := url.Parse(serverURL)
	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: &rewriteRoundTripper{base: base},
	}
}

// fakeProfileHTML returns a stripped-down Nitter profile page with the
// stat blocks the parser cares about.
func fakeProfileHTML(followers, tweets, likes string) string {
	return `<!doctype html><html><head><title>X</title></head><body>
<div class="profile-card">
  <a class="profile-card-fullname" href="/elonmusk">Elon Musk</a>
  <div class="profile-bio"><p>Mars, etc.</p></div>
</div>
<div class="profile-card-extra-links">
  <ul class="profile-statlist">
    <li class="posts">
      <span class="profile-stat-header">Tweets</span>
      <span class="profile-stat-num">` + tweets + `</span>
    </li>
    <li class="following">
      <span class="profile-stat-header">Following</span>
      <span class="profile-stat-num">1,332</span>
    </li>
    <li class="followers">
      <span class="profile-stat-header">Followers</span>
      <span class="profile-stat-num">` + followers + `</span>
    </li>
    <li class="likes">
      <span class="profile-stat-header">Likes</span>
      <span class="profile-stat-num">` + likes + `</span>
    </li>
  </ul>
</div>
</body></html>`
}

const fakeRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>elonmusk / @elonmusk</title>
    <link>https://nitter.example/elonmusk</link>
    <item>
      <title>Hello fresh</title>
      <link>https://nitter.example/elonmusk/status/1700000000000000003#m</link>
      <pubDate>Mon, 18 May 2026 10:00:00 GMT</pubDate>
      <guid>https://nitter.example/elonmusk/status/1700000000000000003</guid>
    </item>
    <item>
      <title>Old tweet</title>
      <link>https://nitter.example/elonmusk/status/1600000000000000002#m</link>
      <pubDate>Fri, 02 Jan 2026 09:00:00 GMT</pubDate>
      <guid>https://nitter.example/elonmusk/status/1600000000000000002</guid>
    </item>
    <item>
      <title>Older tweet</title>
      <link>https://nitter.example/elonmusk/status/1500000000000000001#m</link>
      <pubDate>Sun, 01 Dec 2025 09:00:00 GMT</pubDate>
      <guid>https://nitter.example/elonmusk/status/1500000000000000001</guid>
    </item>
  </channel>
</rss>`

func TestPlatform(t *testing.T) {
	p := New(Config{})
	if got := p.Platform(); got != shared.PlatformX {
		t.Fatalf("Platform: got %s, want %s", got, shared.PlatformX)
	}
}

// happy path: first mirror serves valid HTML, parser returns a populated snapshot.
func TestFetchChannel_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/elonmusk" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(fakeProfileHTML("239,913,818", "102,758", "228,266")))
	}))
	defer srv.Close()

	p := New(Config{
		NitterMirrors: []string{"https://nitter.one.example", "https://nitter.two.example"},
		HTTPClient:    newTestClient(srv.URL),
	})

	snap, err := p.FetchChannel(context.Background(), "@ElonMusk")
	if err != nil {
		t.Fatalf("FetchChannel: %v", err)
	}
	if snap.Handle != "elonmusk" {
		t.Fatalf("handle not normalised: %q", snap.Handle)
	}
	if snap.Followers != 239913818 {
		t.Errorf("Followers: got %d, want 239913818", snap.Followers)
	}
	if snap.PostsCount != 102758 {
		t.Errorf("PostsCount: got %d, want 102758", snap.PostsCount)
	}
	if snap.TotalLikes != 228266 {
		t.Errorf("TotalLikes: got %d, want 228266", snap.TotalLikes)
	}
	if snap.URL != "https://x.com/elonmusk" {
		t.Errorf("URL: got %q", snap.URL)
	}
	if snap.Raw["display_name"] != "Elon Musk" {
		t.Errorf("display_name: %v", snap.Raw["display_name"])
	}
	if !strings.Contains(fmt.Sprint(snap.Raw["bio"]), "Mars") {
		t.Errorf("bio missing: %v", snap.Raw["bio"])
	}
	if snap.FetchedAt.IsZero() {
		t.Errorf("FetchedAt zero")
	}
}

// First mirror 5xxs; second mirror serves valid HTML. Parser must transparently fail over.
func TestFetchChannel_FallsOverToNextMirror(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mirror := r.Header.Get("X-Mirror")
		switch mirror {
		case "nitter.one.example":
			http.Error(w, "bad gateway", http.StatusBadGateway)
		case "nitter.two.example":
			_, _ = w.Write([]byte(fakeProfileHTML("1,000", "10", "5")))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := New(Config{
		NitterMirrors: []string{"https://nitter.one.example", "https://nitter.two.example"},
		HTTPClient:    newTestClient(srv.URL),
	})
	snap, err := p.FetchChannel(context.Background(), "elonmusk")
	if err != nil {
		t.Fatalf("FetchChannel: %v", err)
	}
	if snap.Followers != 1000 {
		t.Errorf("Followers: got %d, want 1000", snap.Followers)
	}
	mirror, _ := snap.Raw["mirror"].(string)
	if mirror != "https://nitter.two.example" {
		t.Errorf("mirror: got %q, want second mirror", mirror)
	}
}

// All mirrors fail → ErrTransient.
func TestFetchChannel_AllMirrorsFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()

	p := New(Config{
		NitterMirrors: []string{"https://a.example", "https://b.example", "https://c.example"},
		HTTPClient:    newTestClient(srv.URL),
	})
	_, err := p.FetchChannel(context.Background(), "elonmusk")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, shared.ErrTransient) {
		t.Fatalf("want ErrTransient, got %v", err)
	}
	if !strings.Contains(err.Error(), "camoufox") {
		t.Errorf("error should hint at camoufox fallback, got %q", err)
	}
}

// 404 with a "User \"x\" not found" page must surface as ErrNotFound and short-circuit.
func TestFetchChannel_NotFoundShortCircuits(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<html><body>User "ghost" not found</body></html>`))
	}))
	defer srv.Close()

	p := New(Config{
		NitterMirrors: []string{"https://a.example", "https://b.example", "https://c.example"},
		HTTPClient:    newTestClient(srv.URL),
	})
	_, err := p.FetchChannel(context.Background(), "ghost")
	if !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if hits != 1 {
		t.Errorf("hits: got %d, want 1 (short-circuit on 404)", hits)
	}
}

// RSS: three items, since-cutoff drops two, only the freshest is returned.
func TestFetchRecentPosts_SinceFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/rss") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(fakeRSS))
	}))
	defer srv.Close()

	p := New(Config{
		NitterMirrors: []string{"https://nitter.one.example"},
		HTTPClient:    newTestClient(srv.URL),
	})

	since := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	posts, err := p.FetchRecentPosts(context.Background(), "elonmusk", since)
	if err != nil {
		t.Fatalf("FetchRecentPosts: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts: got %d, want 1", len(posts))
	}
	p0 := posts[0]
	if p0.PostID != "1700000000000000003" {
		t.Errorf("PostID: got %q", p0.PostID)
	}
	if p0.URL != "https://x.com/elonmusk/status/1700000000000000003" {
		t.Errorf("URL: got %q", p0.URL)
	}
	if p0.Kind != shared.PostKindPost {
		t.Errorf("Kind: got %s", p0.Kind)
	}
	if p0.PublishedAt.IsZero() {
		t.Errorf("PublishedAt zero")
	}
	if p0.ChannelHandle != "elonmusk" {
		t.Errorf("ChannelHandle: got %q", p0.ChannelHandle)
	}
	if p0.Likes != 0 || p0.Views != 0 || p0.Comments != 0 {
		t.Errorf("engagement should be 0 from RSS, got L=%d V=%d C=%d", p0.Likes, p0.Views, p0.Comments)
	}
}

// RSS: zero-value since returns every item.
func TestFetchRecentPosts_NoSinceReturnsAll(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fakeRSS))
	}))
	defer srv.Close()

	p := New(Config{
		NitterMirrors: []string{"https://nitter.one.example"},
		HTTPClient:    newTestClient(srv.URL),
	})
	posts, err := p.FetchRecentPosts(context.Background(), "elonmusk", time.Time{})
	if err != nil {
		t.Fatalf("FetchRecentPosts: %v", err)
	}
	if len(posts) != 3 {
		t.Fatalf("posts: got %d, want 3", len(posts))
	}
}

// Compact unit test on the number parser.
func TestParseNitterNumber(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"1,234", 1234, true},
		{"239,913,818", 239913818, true},
		{"12.3K", 12300, true},
		{"1.2M", 1200000, true},
		{"3B", 3_000_000_000, true},
		{"", 0, false},
		{"-", 0, false},
	}
	for _, c := range cases {
		got, ok := parseNitterNumber(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseNitterNumber(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
