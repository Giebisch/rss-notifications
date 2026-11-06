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
)

//go:embed icon.png
var icon []byte

//go:embed icon_paused.png
var iconPaused []byte

type durationOption struct {
	label string
	d     time.Duration
}

var intervals = []durationOption{
	{"1 min", time.Minute},
	{"3 min", 3 * time.Minute},
	{"5 min", 5 * time.Minute},
	{"10 min", 10 * time.Minute},
	{"15 min", 15 * time.Minute},
	{"30 min", 30 * time.Minute},
	{"1 hour", time.Hour},
}

// A duration of 0 pauses until notifications are resumed manually.
var pauses = []durationOption{
	{"5 min", 5 * time.Minute},
	{"15 min", 15 * time.Minute},
	{"30 min", 30 * time.Minute},
	{"45 min", 45 * time.Minute},
	{"1 hour", time.Hour},
	{"Until Resumed", 0},
}

type app struct {
	store           *store
	menuMu          sync.Mutex
	intervalChanged chan struct{}
	// resumeTimer rebuilds the menu when a timed pause ends.
	resumeTimer *time.Timer
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

	// The app has no terminal, so startup errors are shown as a dialog.
	dir, err := os.UserConfigDir()
	if err != nil {
		showError("Could not start RSS Notifications", err)
		os.Exit(1)
	}
	s, err := openStore(filepath.Join(dir, "RSS Notifications", "feeds.json"))
	if err != nil {
		showError("Could not start RSS Notifications", err)
		os.Exit(1)
	}
	a := &app{store: s, intervalChanged: make(chan struct{}, 1)}
	notifyInit()
	systray.Run(a.onReady, nil)
}

func (a *app) onReady() {
	a.buildMenu()
	go func() {
		for {
			a.check()
			select {
			case <-time.After(a.store.interval()):
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

	feeds := a.store.feeds()
	for _, f := range feeds {
		menu := systray.AddMenuItem(f.displayName(), f.URL)
		website := menu.AddSubMenuItem("Open Website", f.Link)
		mute := menu.AddSubMenuItemCheckbox("Mute", "", f.Muted)
		include := menu.AddSubMenuItemCheckbox("Include Filter…", f.Include, f.Include != "")
		exclude := menu.AddSubMenuItemCheckbox("Exclude Filter…", f.Exclude, f.Exclude != "")
		rename := menu.AddSubMenuItem("Rename…", "")
		remove := menu.AddSubMenuItem("Remove", "")
		if f.Link == "" {
			website.Disable()
		}
		go onClick(website, func() { openURL(f.Link) })
		go onClick(mute, func() { a.toggleMute(f.URL, mute) })
		go onClick(include, func() {
			a.editFilter("Include Filter", "Only notify about items whose title matches this regular expression.",
				"e.g. Go|Rust", f.Include, func(p string) error { return a.store.setInclude(f.URL, p) })
		})
		go onClick(exclude, func() {
			a.editFilter("Exclude Filter", "Don't notify about items whose title matches this regular expression.",
				"e.g. Sponsored|Podcast", f.Exclude, func(p string) error { return a.store.setExclude(f.URL, p) })
		})
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
		options = append(options, refresh.AddSubMenuItemCheckbox(iv.label, "", iv.d == a.store.interval()))
	}
	for i, opt := range options {
		go onClick(opt, func() { a.setInterval(intervals[i].d, options, opt) })
	}
	a.addPauseItems()
	login := systray.AddMenuItemCheckbox("Launch at Login", "", loginItemEnabled())
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "")

	go onClick(add, a.addFeed)
	go onClick(checkNow, a.check)
	go onClick(login, func() { toggleLoginItem(login) })
	go onClick(quit, systray.Quit)
}

// addPauseItems adds the pause or resume entries and dims the menu bar icon while paused.
// It must be called with a.menuMu held.
func (a *app) addPauseItems() {
	if a.resumeTimer != nil {
		a.resumeTimer.Stop()
	}
	paused, until := a.store.pauseState()
	if !paused {
		systray.SetTemplateIcon(icon, icon)
		systray.SetTooltip("RSS Notifications")
		pause := systray.AddMenuItem("Pause Notifications", "")
		for _, p := range pauses {
			go onClick(pause.AddSubMenuItem(p.label, ""), func() { a.pause(p.d) })
		}
		return
	}

	systray.SetTemplateIcon(iconPaused, iconPaused)
	systray.SetTooltip("RSS Notifications (paused)")
	status := "Notifications Paused"
	if !until.IsZero() {
		status += " until " + until.Format("15:04")
		a.resumeTimer = time.AfterFunc(time.Until(until), a.buildMenu)
	}
	systray.AddMenuItem(status, "").Disable()
	go onClick(systray.AddMenuItem("Resume Notifications", ""), a.resume)
}

func onClick(mi *systray.MenuItem, f func()) {
	for range mi.ClickedCh {
		f()
	}
}

func (a *app) check() {
	for _, it := range a.store.check(context.Background()) {
		notify(it.FeedName, it.Title, it.Link, it.FeedURL)
	}
}

func (a *app) addFeed() {
	url, ok := prompt("Add Feed", "Feed URL:", "Add", "https://example.com/feed.xml", "", true)
	url = strings.TrimSpace(url)
	if !ok || url == "" {
		return
	}
	if err := a.store.add(context.Background(), url); err != nil {
		showError("Could not add feed", err)
		return
	}
	a.buildMenu()
}

func (a *app) toggleMute(url string, mute *systray.MenuItem) {
	if err := a.store.setMuted(url, !mute.Checked()); err != nil {
		showError("Could not change mute setting", err)
		return
	}
	if mute.Checked() {
		mute.Uncheck()
	} else {
		mute.Check()
	}
}

func (a *app) editFilter(title, message, placeholder, current string, set func(pattern string) error) {
	pattern, ok := prompt(title, message+" Leave empty to turn the filter off.", "Save", placeholder, current, false)
	if !ok {
		return
	}
	if err := set(pattern); err != nil {
		showError("Invalid filter", err)
		return
	}
	a.buildMenu()
}

func (a *app) renameFeed(f feed) {
	name, ok := prompt("Rename Feed", "Leave empty to use the feed's own title.", "Rename", f.Title, f.displayName(), false)
	if !ok {
		return
	}
	if err := a.store.rename(f.URL, name); err != nil {
		showError("Could not rename feed", err)
		return
	}
	a.buildMenu()
}

func (a *app) removeFeed(url string) {
	if err := a.store.remove(url); err != nil {
		showError("Could not remove feed", err)
		return
	}
	a.buildMenu()
}

func (a *app) setInterval(d time.Duration, options []*systray.MenuItem, selected *systray.MenuItem) {
	if err := a.store.setInterval(d); err != nil {
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

func (a *app) pause(d time.Duration) {
	if err := a.store.pause(d); err != nil {
		showError("Could not pause notifications", err)
		return
	}
	a.buildMenu()
}

func (a *app) resume() {
	if err := a.store.resume(); err != nil {
		showError("Could not resume notifications", err)
		return
	}
	a.buildMenu()
}

func toggleLoginItem(login *systray.MenuItem) {
	if err := setLoginItem(!login.Checked()); err != nil {
		showError("Could not change login item", err)
	}
	if loginItemEnabled() {
		login.Check()
	} else {
		login.Uncheck()
	}
}

func showError(title string, err error) {
	log.Printf("%s: %v", title, err)
	showAlert(title, err.Error())
}
