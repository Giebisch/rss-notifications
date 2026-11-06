#!/usr/bin/env python3
"""Runs the installed app with demo feeds for recording the README video.

Swaps feeds.json for a demo config, serves the demo feeds locally and publishes a
new item on every Enter. Ctrl+C restores the real config and relaunches the app.
"""

import functools
import http.server
import json
import subprocess
import tempfile
import threading
import time
from email.utils import formatdate
from pathlib import Path
from xml.sax.saxutils import escape

PORT = 8765
CONFIG = Path.home() / "Library/Application Support/RSS Notifications/feeds.json"
BACKUP = CONFIG.with_name("feeds.json.demo-backup")

# The first feed receives the items published with Enter.
FEEDS = [
    {
        "name": "Engineering Blog",
        "link": "https://example.com/engineering",
        "items": [
            "Scaling our build pipeline to 10,000 jobs a day",
            "What we learned migrating to a monorepo",
            "Designing APIs that are hard to misuse",
        ],
    },
    {
        "name": "Release Notes",
        "link": "https://example.com/releases",
        "include": "(?i)release",
        "items": ["Version 2.4 released", "Version 2.3.1 security release"],
    },
    {
        "name": "Design Weekly",
        "link": "https://example.com/design",
        "muted": True,
        "items": ["Typography on small screens", "A case for fewer settings"],
    },
]
NEW_ITEMS = [
    "Why we rewrote our scheduler in Go",
    "Shipping native macOS apps without Xcode projects",
    "Testing time-dependent code without sleeping",
]


def slug(title):
    return "".join(c if c.isalnum() else "-" for c in title.lower()).strip("-")


def write_feed(path, feed, titles):
    items = "".join(
        f"<item><title>{escape(t)}</title><link>{feed['link']}/{slug(t)}</link>"
        f"<guid>{feed['link']}/{slug(t)}</guid><pubDate>{formatdate()}</pubDate></item>"
        for t in titles
    )
    path.write_text(
        '<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel>'
        f"<title>{escape(feed['name'])}</title><link>{feed['link']}</link>"
        f"<description>Demo feed</description>{items}</channel></rss>"
    )


class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *args):
        pass


def stop_app():
    subprocess.run(["pkill", "-x", "rss-notifications"])
    while subprocess.run(["pgrep", "-x", "rss-notifications"], capture_output=True).returncode == 0:
        time.sleep(0.2)


def start_app():
    # Launch Services refuses to open the app (error -600) for a moment after it was killed.
    for _ in range(10):
        if subprocess.run(["open", "-a", "RSS Notifications"], capture_output=True).returncode == 0:
            return
        time.sleep(0.5)
    raise SystemExit("Could not start RSS Notifications.")


def main():
    if BACKUP.exists():
        raise SystemExit(f"{BACKUP} exists from an earlier run. Restore or remove it first.")

    serve_dir = Path(tempfile.mkdtemp())
    config = {"interval_minutes": 60, "feeds": []}
    for i, feed in enumerate(FEEDS):
        write_feed(serve_dir / f"{i}.xml", feed, feed["items"])
        config["feeds"].append({
            "url": f"http://127.0.0.1:{PORT}/{i}.xml",
            "title": feed["name"],
            "link": feed["link"],
            "muted": feed.get("muted", False),
            "include": feed.get("include", ""),
            "seen": [f"{feed['link']}/{slug(t)}" for t in feed["items"]],
        })

    server = http.server.ThreadingHTTPServer(("127.0.0.1", PORT), functools.partial(QuietHandler, directory=serve_dir))
    threading.Thread(target=server.serve_forever, daemon=True).start()

    stop_app()
    had_config = CONFIG.exists()
    if had_config:
        CONFIG.rename(BACKUP)
    try:
        CONFIG.parent.mkdir(parents=True, exist_ok=True)
        CONFIG.write_text(json.dumps(config, indent=2))
        start_app()

        published = list(FEEDS[0]["items"])
        for title in NEW_ITEMS:
            input(f"Press Enter to publish \"{title}\", then click Check Now (Ctrl+C to stop) ")
            published.insert(0, title)
            write_feed(serve_dir / "0.xml", FEEDS[0], published)
        print("All demo items published. Press Ctrl+C to stop.")
        threading.Event().wait()
    except (KeyboardInterrupt, EOFError):
        print()
    finally:
        stop_app()
        if had_config:
            BACKUP.replace(CONFIG)
        else:
            CONFIG.unlink(missing_ok=True)
        server.shutdown()
        start_app()
        print("Restored your feeds.")


if __name__ == "__main__":
    main()
