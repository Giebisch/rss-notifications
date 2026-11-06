APP := build/RSS Notifications.app
BIN := rss-notifications

.PHONY: build run install test clean

# Notifications only work from a signed .app bundle; ad-hoc signing is enough.
build:
	mkdir -p "$(APP)/Contents/MacOS"
	go build -o "$(APP)/Contents/MacOS/$(BIN)" .
	cp Info.plist "$(APP)/Contents/Info.plist"
	codesign --force --sign - "$(APP)"

run: build
	pkill -x $(BIN) || true
	open "$(APP)"

install: build
	rm -rf "/Applications/RSS Notifications.app"
	cp -R "$(APP)" /Applications/

test:
	go vet ./...
	go test ./...

clean:
	rm -rf build
