package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/ncruces/zenity"
)

//go:embed icon.png
var icon []byte

var intervals = []struct {
	label string
	d     time.Duration
}{
	{"1 min", time.Minute},
	{"3 min", 3 * time.Minute},
	{"5 min", 5 * time.Minute},
	{"10 min", 10 * time.Minute},
	{"15 min", 15 * time.Minute},
	{"30 min", 30 * time.Minute},
	{"1 hour", time.Hour},
}

type app struct {
	store           *Store
	menuMu          sync.Mutex
	intervalChanged chan struct{}
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "install" {
		app, err := install()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Installed %s\nIf the app was already running, quit it from the menu bar to use the new version.\n", app)
		if err := exec.Command("open", app).Run(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if !inAppBundle() {
		fmt.Fprintln(os.Stderr, "rss-notifications only works as a macOS app bundle.\nRun 'rss-notifications install' to install it to ~/Applications.")
		os.Exit(2)
	}

	dir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	store, err := OpenStore(filepath.Join(dir, "RSS Notifications", "feeds.json"))
	if err != nil {
		log.Fatal(err)
	}
	a := &app{store: store, intervalChanged: make(chan struct{}, 1)}
	notifyInit()
	systray.Run(a.onReady, nil)
}

func (a *app) onReady() {
	systray.SetTemplateIcon(icon, icon)
	systray.SetTooltip("RSS Notifications")
	a.buildMenu()
	go func() {
		for {
			a.check()
			select {
			case <-time.After(a.store.Interval()):
			case <-a.intervalChanged:
			}
		}
	}()
}

func (a *app) buildMenu() {
	a.menuMu.Lock()
	defer a.menuMu.Unlock()
	// Removing the items closes their ClickedCh, which ends the onClick goroutines of the previous menu.
	systray.ResetMenu()

	feeds := a.store.Feeds()
	for _, f := range feeds {
		item := systray.AddMenuItem(f.DisplayName(), f.URL)
		website := item.AddSubMenuItem("Open Website", f.Link)
		rename := item.AddSubMenuItem("Rename…", "")
		remove := item.AddSubMenuItem("Remove", "")
		if f.Link == "" {
			website.Disable()
		}
		go onClick(website, func() { openURL(f.Link) })
		go onClick(rename, func() { a.renameFeed(f) })
		go onClick(remove, func() { a.removeFeed(f.URL) })
	}
	if len(feeds) == 0 {
		systray.AddMenuItem("No feeds", "").Disable()
	}
	systray.AddSeparator()
	add := systray.AddMenuItem("Add Feed…", "")
	checkNow := systray.AddMenuItem("Check Now", "")
	refresh := systray.AddMenuItem("Refresh Every", "")
	var options []*systray.MenuItem
	for _, iv := range intervals {
		options = append(options, refresh.AddSubMenuItemCheckbox(iv.label, "", iv.d == a.store.Interval()))
	}
	for i, opt := range options {
		go onClick(opt, func() { a.setInterval(intervals[i].d, options, opt) })
	}
	login := systray.AddMenuItemCheckbox("Launch at Login", "", loginItemEnabled())
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "")

	go onClick(add, a.addFeed)
	go onClick(checkNow, a.check)
	go onClick(login, func() { toggleLoginItem(login) })
	go onClick(quit, systray.Quit)
}

func onClick(item *systray.MenuItem, f func()) {
	for range item.ClickedCh {
		f()
	}
}

func (a *app) check() {
	for _, it := range a.store.Check(context.Background()) {
		notify(it.FeedTitle, it.Title, it.Link, it.FeedURL)
	}
}

func (a *app) addFeed() {
	url, ok := prompt("Add Feed", "Feed URL:", "Add", "https://example.com/feed.xml", "", true)
	url = strings.TrimSpace(url)
	if !ok || url == "" {
		return
	}
	if err := a.store.Add(context.Background(), url); err != nil {
		showError("Could not add feed", err)
		return
	}
	a.buildMenu()
}

func (a *app) renameFeed(f Feed) {
	name, ok := prompt("Rename Feed", "Leave empty to use the feed's own title.", "Rename", f.Title, f.DisplayName(), false)
	if !ok {
		return
	}
	if err := a.store.Rename(f.URL, name); err != nil {
		showError("Could not rename feed", err)
		return
	}
	a.buildMenu()
}

func (a *app) removeFeed(url string) {
	if err := a.store.Remove(url); err != nil {
		showError("Could not remove feed", err)
		return
	}
	a.buildMenu()
}

func (a *app) setInterval(d time.Duration, options []*systray.MenuItem, selected *systray.MenuItem) {
	if err := a.store.SetInterval(d); err != nil {
		showError("Could not change refresh interval", err)
		return
	}
	for _, opt := range options {
		opt.Uncheck()
	}
	selected.Check()
	select {
	case a.intervalChanged <- struct{}{}:
	default:
	}
}

func toggleLoginItem(item *systray.MenuItem) {
	if err := setLoginItem(!item.Checked()); err != nil {
		showError("Could not change login item", err)
	}
	if loginItemEnabled() {
		item.Check()
	} else {
		item.Uncheck()
	}
}

func showError(title string, err error) {
	log.Printf("%s: %v", title, err)
	_ = zenity.Error(err.Error(), zenity.Title(title))
}
