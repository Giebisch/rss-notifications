package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Add(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(ctx, srv.URL); err == nil {
		t.Error("adding a duplicate feed should fail")
	}
	if got := store.Check(ctx); len(got) != 0 {
		t.Fatalf("existing items reported as new: %+v", got)
	}
	if err := store.Rename(srv.URL, " Custom "); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	items = []string{"c", "a", "b"}
	mu.Unlock()

	// Reopen to verify the seen state and name were persisted.
	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := store.Check(ctx)
	want := Item{FeedURL: srv.URL, FeedTitle: "Custom", Title: "Item c", Link: "https://example.com/c"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %+v, want [%+v]", got, want)
	}
	if got := store.Check(ctx); len(got) != 0 {
		t.Fatalf("item reported twice: %+v", got)
	}

	if err := store.Rename(srv.URL, ""); err != nil {
		t.Fatal(err)
	}
	if got := store.Feeds()[0].DisplayName(); got != "Test" {
		t.Errorf("empty name should fall back to the feed title, got %q", got)
	}
}
