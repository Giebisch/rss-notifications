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
)

func TestCheckReportsOnlyNewItems(t *testing.T) {
	var mu sync.Mutex
	items := []string{"a", "b"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var b strings.Builder
		b.WriteString(`<rss version="2.0"><channel><title>Test</title><link>https://example.com</link>`)
		for _, id := range items {
			fmt.Fprintf(&b, `<item><guid>%s</guid><title>Item %s</title><link>https://example.com/%s</link></item>`, id, id, id)
		}
		b.WriteString(`</channel></rss>`)
		w.Write([]byte(b.String()))
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "feeds.json")
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.add(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := s.add(ctx, srv.URL); err == nil {
		t.Error("adding a duplicate feed should fail")
	}
	if got := s.check(ctx); len(got) != 0 {
		t.Fatalf("existing items reported as new: %+v", got)
	}
	if err := s.rename(srv.URL, " Custom "); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	items = []string{"c", "a", "b"}
	mu.Unlock()

	// Reopen to verify the seen state and name were persisted.
	s, err = openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := s.check(ctx)
	want := item{FeedURL: srv.URL, FeedName: "Custom", Title: "Item c", Link: "https://example.com/c"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v, want [%+v]", got, want)
	}
	if got := s.check(ctx); len(got) != 0 {
		t.Fatalf("item reported twice: %+v", got)
	}

	if err := s.setMuted(srv.URL, true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	items = []string{"d", "c", "a", "b"}
	mu.Unlock()
	if got := s.check(ctx); len(got) != 0 {
		t.Fatalf("muted feed reported items: %+v", got)
	}
	if err := s.setMuted(srv.URL, false); err != nil {
		t.Fatal(err)
	}
	if got := s.check(ctx); len(got) != 0 {
		t.Fatalf("items published while muted reported after unmuting: %+v", got)
	}

	if err := s.rename(srv.URL, ""); err != nil {
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
