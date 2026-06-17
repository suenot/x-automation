package parser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	shared "github.com/suenot/socials-auto"
)

// ---------- 1. API v2 happy path -------------------------------------------

func TestFetchChannel_APIv2_HappyPath(t *testing.T) {
	const handle = "elonmusk"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("Authorization"), "Bearer test-token"; got != want {
			t.Errorf("Authorization header: got %q, want %q", got, want)
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/2/users/by/username/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if !strings.HasSuffix(r.URL.Path, "/"+handle) {
			t.Errorf("unexpected handle in path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"data": {
				"id": "44196397",
				"name": "Elon Musk",
				"username": "elonmusk",
				"description": "tech things",
				"created_at": "2009-06-02T20:12:29.000Z",
				"verified": true,
				"public_metrics": {
					"followers_count": 239913818,
					"following_count": 1332,
					"tweet_count": 102758,
					"listed_count": 156000,
					"like_count": 228266
				}
			}
		}`))
	}))
	defer srv.Close()

	p := New(Config{
		BearerToken: "test-token",
		APIBaseURL:  srv.URL,
	})
	snap, err := p.FetchChannel(context.Background(), "@ElonMusk")
	if err != nil {
		t.Fatalf("FetchChannel: %v", err)
	}
	if snap.Handle != "elonmusk" {
		t.Errorf("handle: got %q", snap.Handle)
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
	if got := snap.Raw["source"]; got != "api_v2" {
		t.Errorf("Raw[source]: got %v", got)
	}
	if got := snap.Raw["name"]; got != "Elon Musk" {
		t.Errorf("Raw[name]: got %v", got)
	}
	if got := snap.Raw["verified"]; got != true {
		t.Errorf("Raw[verified]: got %v", got)
	}
	if got, _ := snap.Raw["following_count"].(int64); got != 1332 {
		t.Errorf("Raw[following_count]: got %v", snap.Raw["following_count"])
	}
	if snap.FetchedAt.IsZero() {
		t.Errorf("FetchedAt zero")
	}
}

// ---------- 2. Syndication happy path --------------------------------------

func TestFetchChannel_Syndication_HappyPath(t *testing.T) {
	// Embedded HTML carrying the __INITIAL_STATE__ blob the parser
	// looks for. Keep the JSON inline-friendly: no nested backticks.
	embeddedHTML := `<!doctype html><html><body><script>
__INITIAL_STATE__ = {
  "user": {
    "id_str": "44196397",
    "screen_name": "elonmusk",
    "name": "Elon Musk",
    "description": "to Mars",
    "created_at": "Tue Jun 02 20:12:29 +0000 2009",
    "verified": true,
    "followers_count": 240000001,
    "friends_count": 1332,
    "statuses_count": 102800,
    "favourites_count": 228300,
    "listed_count": 156000
  }
};
</script></body></html>`

	envelope := map[string]any{"body": embeddedHTML}
	envJSON, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}

	syndicationCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		syndicationCalled = true
		if !strings.HasPrefix(r.URL.Path, "/timeline/profile") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("screen_name") != "elonmusk" {
			t.Errorf("screen_name: got %q", r.URL.Query().Get("screen_name"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(envJSON)
	}))
	defer srv.Close()

	p := New(Config{
		// No bearer token → API path skipped.
		SyndicationURL: srv.URL,
		// fxtwitter is not consulted because syndication succeeds.
		FxTwitterURL: "http://127.0.0.1:1", // would fail if hit
	})

	snap, err := p.FetchChannel(context.Background(), "elonmusk")
	if err != nil {
		t.Fatalf("FetchChannel: %v", err)
	}
	if !syndicationCalled {
		t.Fatal("syndication endpoint was not called")
	}
	if snap.Followers != 240000001 {
		t.Errorf("Followers: got %d, want 240000001", snap.Followers)
	}
	if snap.PostsCount != 102800 {
		t.Errorf("PostsCount: got %d, want 102800", snap.PostsCount)
	}
	if snap.TotalLikes != 228300 {
		t.Errorf("TotalLikes: got %d, want 228300", snap.TotalLikes)
	}
	if got := snap.Raw["source"]; got != "syndication" {
		t.Errorf("Raw[source]: got %v", got)
	}
	if got := snap.Raw["id"]; got != "44196397" {
		t.Errorf("Raw[id]: got %v", got)
	}
}

// ---------- 3. All public paths exhausted → ErrAuth + camoufox hint --------

func TestFetchChannel_AllPathsFail(t *testing.T) {
	// Syndication serves empty body (mirrors the real-world degraded
	// state). fxtwitter returns 502.
	syndSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// empty body
	}))
	defer syndSrv.Close()

	fxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer fxSrv.Close()

	p := New(Config{
		SyndicationURL: syndSrv.URL,
		FxTwitterURL:   fxSrv.URL,
	})

	_, err := p.FetchChannel(context.Background(), "elonmusk")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, shared.ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
	if !strings.Contains(err.Error(), "camoufox") {
		t.Errorf("expected camoufox hint in error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "X_BEARER_TOKEN") {
		t.Errorf("expected token hint in error, got %q", err.Error())
	}
}

// ---------- 4. API v2 returns 404-style error → ErrNotFound ----------------

func TestFetchChannel_APIv2_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// X API v2 actually returns 200 + errors[] for "no such user".
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"errors": [{
				"value": "ghost",
				"detail": "Could not find user with username: [ghost].",
				"title": "Not Found Error",
				"resource_type": "user",
				"parameter": "username",
				"resource_id": "ghost",
				"type": "https://api.twitter.com/2/problems/resource-not-found"
			}]
		}`))
	}))
	defer srv.Close()

	p := New(Config{
		BearerToken: "test-token",
		APIBaseURL:  srv.URL,
	})

	_, err := p.FetchChannel(context.Background(), "ghost")
	if !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// ---------- 4b. HTTP-404 also surfaces ErrNotFound -------------------------

func TestFetchChannel_APIv2_HTTPNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p := New(Config{
		BearerToken: "test-token",
		APIBaseURL:  srv.URL,
	})

	_, err := p.FetchChannel(context.Background(), "ghost")
	if !errors.Is(err, shared.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// ---------- 5. 429 → ErrRateLimited ----------------------------------------

func TestFetchChannel_APIv2_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"title":"Too Many Requests","detail":"Rate limit exceeded"}`))
	}))
	defer srv.Close()

	p := New(Config{
		BearerToken: "test-token",
		APIBaseURL:  srv.URL,
	})

	_, err := p.FetchChannel(context.Background(), "elonmusk")
	if !errors.Is(err, shared.ErrRateLimited) {
		t.Fatalf("want ErrRateLimited, got %v", err)
	}
}

// ---------- Extras ---------------------------------------------------------

// fxtwitter path is consulted when API isn't configured and syndication is
// empty / malformed.
func TestFetchChannel_FxTwitterFallback(t *testing.T) {
	syndSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Empty body → parser falls through to fxtwitter.
	}))
	defer syndSrv.Close()

	fxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code": 200,
			"message": "OK",
			"user": {
				"screen_name": "elonmusk",
				"url": "https://x.com/elonmusk",
				"id": "44196397",
				"followers": 239920321,
				"following": 1332,
				"likes": 228287,
				"media_count": 4497,
				"tweets": 102762,
				"name": "Elon Musk",
				"description": "things",
				"joined": "Tue Jun 02 20:12:29 +0000 2009",
				"protected": false,
				"verification": { "verified": true, "type": "individual" }
			}
		}`))
	}))
	defer fxSrv.Close()

	p := New(Config{
		SyndicationURL: syndSrv.URL,
		FxTwitterURL:   fxSrv.URL,
	})
	snap, err := p.FetchChannel(context.Background(), "elonmusk")
	if err != nil {
		t.Fatalf("FetchChannel: %v", err)
	}
	if snap.Followers != 239920321 {
		t.Errorf("Followers: got %d, want 239920321", snap.Followers)
	}
	if snap.PostsCount != 102762 {
		t.Errorf("PostsCount: got %d, want 102762", snap.PostsCount)
	}
	if snap.TotalLikes != 228287 {
		t.Errorf("TotalLikes: got %d, want 228287", snap.TotalLikes)
	}
	if got := snap.Raw["source"]; got != "fxtwitter" {
		t.Errorf("Raw[source]: got %v", got)
	}
}

func TestFetchRecentPosts_NoBearerReturnsEmpty(t *testing.T) {
	p := New(Config{})
	posts, err := p.FetchRecentPosts(context.Background(), "elonmusk", time.Time{})
	if err != nil {
		t.Fatalf("FetchRecentPosts: %v", err)
	}
	if len(posts) != 0 {
		t.Errorf("posts: got %d, want 0", len(posts))
	}
}

func TestFetchRecentPosts_APIv2(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/2/users/by/username/"):
			_, _ = w.Write([]byte(`{
				"data": {
					"id": "44196397",
					"name": "Elon",
					"username": "elonmusk",
					"public_metrics": {
						"followers_count": 1,
						"following_count": 1,
						"tweet_count": 1,
						"listed_count": 1,
						"like_count": 1
					}
				}
			}`))
		case strings.HasPrefix(r.URL.Path, "/2/users/44196397/tweets"):
			_, _ = w.Write([]byte(`{
				"data": [
					{
						"id": "1700000000000000003",
						"text": "fresh",
						"created_at": "2026-05-18T10:00:00.000Z",
						"public_metrics": {
							"retweet_count": 10,
							"reply_count": 20,
							"like_count": 300,
							"quote_count": 4,
							"bookmark_count": 5,
							"impression_count": 100000
						}
					},
					{
						"id": "1600000000000000002",
						"text": "old",
						"created_at": "2026-01-02T09:00:00.000Z",
						"public_metrics": {
							"retweet_count": 1,
							"reply_count": 2,
							"like_count": 3,
							"quote_count": 0,
							"bookmark_count": 0,
							"impression_count": 10
						}
					}
				]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := New(Config{
		BearerToken: "test-token",
		APIBaseURL:  srv.URL,
	})

	since := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	posts, err := p.FetchRecentPosts(context.Background(), "elonmusk", since)
	if err != nil {
		t.Fatalf("FetchRecentPosts: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("posts: got %d, want 1 (since-filter)", len(posts))
	}
	got := posts[0]
	if got.PostID != "1700000000000000003" {
		t.Errorf("PostID: got %q", got.PostID)
	}
	if got.URL != "https://x.com/elonmusk/status/1700000000000000003" {
		t.Errorf("URL: got %q", got.URL)
	}
	if got.Likes != 300 {
		t.Errorf("Likes: got %d, want 300", got.Likes)
	}
	if got.Views != 100000 {
		t.Errorf("Views: got %d, want 100000", got.Views)
	}
	if got.Comments != 20 {
		t.Errorf("Comments: got %d, want 20", got.Comments)
	}
	if got.Shares != 14 { // retweets + quotes
		t.Errorf("Shares: got %d, want 14", got.Shares)
	}
	if got.Kind != shared.PostKindPost {
		t.Errorf("Kind: got %s", got.Kind)
	}
	if got.PublishedAt.IsZero() {
		t.Errorf("PublishedAt zero")
	}
}

func TestPlatform(t *testing.T) {
	if got := New(Config{}).Platform(); got != shared.PlatformX {
		t.Fatalf("Platform: got %s", got)
	}
}

func TestNormaliseHandle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"@ElonMusk", "elonmusk"},
		{" Elonmusk ", "elonmusk"},
		{"elonmusk", "elonmusk"},
		{"", ""},
	}
	for _, c := range cases {
		if got := normaliseHandle(c.in); got != c.want {
			t.Errorf("normaliseHandle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
