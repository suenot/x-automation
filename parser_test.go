package parser_test

import (
	"context"
	shared "github.com/suenot/socials-auto"
	parser "github.com/suenot/x-automation"
	"testing"
	"time"
)

// Existing import consumers still receive the historical parser API.
func TestCompatibilityFacade(t *testing.T) {
	var p interface {
		Platform() shared.Platform
		FetchChannel(context.Context, string) (shared.ChannelSnapshot, error)
		FetchRecentPosts(context.Context, string, time.Time) ([]shared.PostSnapshot, error)
	} = parser.New(parser.Config{})
	if p.Platform() != shared.PlatformX || parser.DefaultUserAgent == "" {
		t.Fatal("compatibility facade changed")
	}
}
