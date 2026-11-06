package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// feedServer serves an RSS feed with one item per id; set replaces the items.
func feedServer(t *testing.T, ids ...string) (url string, set func(ids ...string)) {
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var b strings.Builder
		b.WriteString(`<rss version="2.0"><channel><title>Test</title><link>https://example.com</link>`)
		for _, id := range ids {
			fmt.Fprintf(&b, `<item><guid>%s</guid><title>Item %s</title><link>https://example.com/%s</link></item>`, id, id, id)
		}
		b.WriteString(`</channel></rss>`)
		w.Write([]byte(b.String()))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func(newIDs ...string) {
		mu.Lock()
		defer mu.Unlock()
		ids = newIDs
	}
}

func TestCheckReportsOnlyNewItems(t *testing.T) {
	url, setItems := feedServer(t, "a", "b")

	path := filepath.Join(t.TempDir(), "feeds.json")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.add(ctx, url); err != nil {
		t.Fatal(err)
	}
	if err := s.add(ctx, url); err == nil {
		t.Error("adding a duplicate feed should fail")
	}
	if got := s.check(ctx); len(got) != 0 {
		t.Fatalf("existing items reported as new: %+v", got)
	}
	if err := s.rename(url, " Custom "); err != nil {
		t.Fatal(err)
	}

	setItems("c", "a", "b")

	// Reopen to verify the seen state and name were persisted.
	s, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := s.check(ctx)
	want := item{FeedURL: url, FeedName: "Custom", Title: "Item c", Link: "https://example.com/c"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v, want [%+v]", got, want)
	}
	if got := s.check(ctx); len(got) != 0 {
		t.Fatalf("item reported twice: %+v", got)
	}

	if err := s.setMuted(url, true); err != nil {
		t.Fatal(err)
	}
	setItems("d", "c", "a", "b")
	if got := s.check(ctx); len(got) != 0 {
		t.Fatalf("muted feed reported items: %+v", got)
	}
	if err := s.setMuted(url, false); err != nil {
		t.Fatal(err)
	}
	if got := s.check(ctx); len(got) != 0 {
		t.Fatalf("items published while muted reported after unmuting: %+v", got)
	}

	if err := s.rename(url, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.feeds()[0].displayName(); got != "Test" {
		t.Errorf("empty name should fall back to the feed title, got %q", got)
	}
}

func TestFeedWants(t *testing.T) {
	tests := []struct {
		include, exclude, title string
		want                    bool
	}{
		{"", "", "anything", true},
		{"Go|Rust", "", "Go 1.25 released", true},
		{"Go|Rust", "", "Python news", false},
		{"Go", "", "go is lowercase", false},
		{"(?i)go", "", "go is lowercase", true},
		{"", "Sponsored", "Sponsored: buy this", false},
		{"Go", "Sponsored", "Sponsored: Go course", false},
		{"Go", "Sponsored", "Go generics", true},
	}
	for _, tt := range tests {
		f := feed{Include: tt.include, Exclude: tt.exclude}
		if got := f.wants(tt.title); got != tt.want {
			t.Errorf("include %q, exclude %q, title %q: got %v, want %v", tt.include, tt.exclude, tt.title, got, tt.want)
		}
	}
}

func TestInvalidFilter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feeds.json")
	s := &store{path: path, cfg: config{Feeds: []feed{{URL: "https://example.com/feed"}}}}
	if err := s.setInclude("https://example.com/feed", "(unclosed"); err == nil {
		t.Error("invalid include pattern was accepted")
	}

	// Hand-edited invalid patterns are rejected on load instead of panicking later.
	if err := os.WriteFile(path, []byte(`{"feeds": [{"url": "u", "exclude": "["}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openStore(path); err == nil {
		t.Error("store with an invalid pattern was loaded")
	}
}

func TestPause(t *testing.T) {
	url, setItems := feedServer(t, "a")
	s, err := openStore(filepath.Join(t.TempDir(), "feeds.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.add(ctx, url); err != nil {
		t.Fatal(err)
	}

	if err := s.pause(0); err != nil {
		t.Fatal(err)
	}
	if paused, until := s.pauseState(); !paused || !until.IsZero() {
		t.Fatalf("pauseState() = %v, %v; want indefinite pause", paused, until)
	}
	setItems("b", "a")
	if got := s.check(ctx); len(got) != 0 {
		t.Fatalf("items reported while paused: %+v", got)
	}
	if err := s.resume(); err != nil {
		t.Fatal(err)
	}
	if got := s.check(ctx); len(got) != 0 {
		t.Fatalf("items published while paused reported after resuming: %+v", got)
	}

	if err := s.pause(time.Hour); err != nil {
		t.Fatal(err)
	}
	if paused, until := s.pauseState(); !paused || time.Until(until) <= 0 {
		t.Fatalf("pauseState() = %v, %v; want pause for an hour", paused, until)
	}
	s.cfg.PausedUntil = time.Now().Add(-time.Second)
	setItems("c", "b", "a")
	if paused, _ := s.pauseState(); paused {
		t.Fatal("expired pause still active")
	}
	if got := s.check(ctx); len(got) != 1 {
		t.Fatalf("got %d items after the pause expired, want 1", len(got))
	}
}
