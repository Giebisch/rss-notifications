package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
)

const (
	fetchTimeout = 30 * time.Second
	// Bounds the seen list while still remembering items that briefly drop out of a feed
	// (e.g. a stale CDN copy), which would otherwise be reported again.
	maxSeen = 1000
)

type feed struct {
	URL   string `json:"url"`
	Title string `json:"title"`
	// Name is set by the user and overrides Title.
	Name string `json:"name,omitempty"`
	// Muted feeds are still checked, so unmuting doesn't report the items published in the meantime.
	Muted bool     `json:"muted,omitempty"`
	Link  string   `json:"link"`
	Seen  []string `json:"seen"`
}

func (f feed) displayName() string {
	if f.Name != "" {
		return f.Name
	}
	return f.Title
}

type item struct {
	FeedURL  string
	FeedName string
	Title    string
	Link     string
}

type config struct {
	IntervalMinutes int    `json:"interval_minutes"`
	Feeds           []feed `json:"feeds"`
}

type store struct {
	mu     sync.Mutex
	path   string
	cfg    config
	parser *gofeed.Parser
}

func openStore(path string) (*store, error) {
	s := &store{path: path, cfg: config{IntervalMinutes: 10}, parser: gofeed.NewParser()}
	s.parser.UserAgent = "rss-notifications/1.0"
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return s, nil
}

func (s *store) interval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Duration(max(s.cfg.IntervalMinutes, 1)) * time.Minute
}

func (s *store) setInterval(d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.IntervalMinutes = int(d / time.Minute)
	return s.save()
}

func (s *store) feeds() []feed {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.cfg.Feeds)
}

// add fetches the feed and marks its current items as seen, so adding a feed doesn't trigger notifications.
func (s *store) add(ctx context.Context, url string) error {
	parsed, err := s.fetch(ctx, url)
	if err != nil {
		return err
	}
	f := feed{URL: url, Title: parsed.Title, Link: parsed.Link}
	if f.Title == "" {
		f.Title = url
	}
	f.Seen, _ = diff(nil, parsed.Items)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.index(url) >= 0 {
		return errors.New("feed already added")
	}
	s.cfg.Feeds = append(s.cfg.Feeds, f)
	return s.save()
}

// rename sets the feed's name; an empty name falls back to the feed's own title.
func (s *store) rename(url, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(url)
	if i < 0 {
		return errors.New("feed not found")
	}
	s.cfg.Feeds[i].Name = strings.TrimSpace(name)
	return s.save()
}

func (s *store) setMuted(url string, muted bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(url)
	if i < 0 {
		return errors.New("feed not found")
	}
	s.cfg.Feeds[i].Muted = muted
	return s.save()
}

func (s *store) remove(url string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.Feeds = slices.DeleteFunc(s.cfg.Feeds, func(f feed) bool { return f.URL == url })
	return s.save()
}

// check fetches all feeds and returns the items not seen before.
func (s *store) check(ctx context.Context) []item {
	var items []item
	for _, f := range s.feeds() {
		parsed, err := s.fetch(ctx, f.URL)
		if err != nil {
			log.Printf("fetch %s: %v", f.URL, err)
			continue
		}
		// Diff under the lock against the stored state, so concurrent checks never report an item twice.
		s.mu.Lock()
		if i := s.index(f.URL); i >= 0 {
			stored := &s.cfg.Feeds[i]
			var fresh []*gofeed.Item
			stored.Seen, fresh = diff(stored.Seen, parsed.Items)
			if stored.Muted {
				fresh = nil
			}
			for _, it := range fresh {
				link := it.Link
				if link == "" {
					link = stored.Link
				}
				items = append(items, item{FeedURL: stored.URL, FeedName: stored.displayName(), Title: it.Title, Link: link})
			}
		}
		s.mu.Unlock()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.save(); err != nil {
		log.Printf("save: %v", err)
	}
	return items
}

func (s *store) fetch(ctx context.Context, url string) (*gofeed.Feed, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	return s.parser.ParseURLWithContext(url, ctx)
}

// index must be called with s.mu held.
func (s *store) index(url string) int {
	return slices.IndexFunc(s.cfg.Feeds, func(f feed) bool { return f.URL == url })
}

// save must be called with s.mu held.
func (s *store) save() error {
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// diff returns the updated seen list and the items that are not in seen.
func diff(seen []string, items []*gofeed.Item) ([]string, []*gofeed.Item) {
	var ids []string
	var fresh []*gofeed.Item
	for _, it := range items {
		id := itemID(it)
		if slices.Contains(ids, id) {
			continue
		}
		ids = append(ids, id)
		if !slices.Contains(seen, id) {
			fresh = append(fresh, it)
		}
	}
	for _, id := range seen {
		if len(ids) >= maxSeen {
			break
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, fresh
}

func itemID(it *gofeed.Item) string {
	switch {
	case it.GUID != "":
		return it.GUID
	case it.Link != "":
		return it.Link
	default:
		return it.Title
	}
}
