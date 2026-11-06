package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed Info.plist
var infoPlist []byte

// Must match CFBundleExecutable in Info.plist.
const bundleExecutable = "rss-notifications"

func inAppBundle() bool {
	exe, err := os.Executable()
	return err == nil && strings.Contains(exe, ".app/Contents/MacOS/")
}

// install copies the running binary into an ad-hoc signed app bundle in ~/Applications,
// because macOS only delivers notifications for signed app bundles.
func install() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	app := filepath.Join(home, "Applications", "RSS Notifications.app")
	macOS := filepath.Join(app, "Contents", "MacOS")
	if err := os.MkdirAll(macOS, 0o755); err != nil {
		return "", err
	}

	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		return "", err
	}
	// Write and rename, so a running instance keeps its old binary.
	dst := filepath.Join(macOS, bundleExecutable)
	if err := os.WriteFile(dst+".tmp", data, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(dst+".tmp", dst); err != nil {
		return "", err
	}

	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), infoPlist, 0o644); err != nil {
		return "", err
	}
	if out, err := exec.Command("codesign", "--force", "--sign", "-", app).CombinedOutput(); err != nil {
		return "", fmt.Errorf("codesign: %v: %s", err, out)
	}
	return app, nil
}
