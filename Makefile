APP := build/RSS Notifications.app
BIN := rss-notifications

.PHONY: build run install test clean

# Notifications only work from a signed .app bundle; ad-hoc signing is enough.
build:
	mkdir -p "$(APP)/Contents/MacOS" "$(APP)/Contents/Resources"
	go build -o "$(APP)/Contents/MacOS/$(BIN)" .
	cp Info.plist "$(APP)/Contents/Info.plist"
	cp AppIcon.icns "$(APP)/Contents/Resources/AppIcon.icns"
	codesign --force --sign - "$(APP)"

run: build
	pkill -x $(BIN) && sleep 1 || true
	open "$(APP)"

install:
	go build -o build/$(BIN) .
	build/$(BIN) install

test:
	go vet ./...
	go test ./...

clean:
	rm -rf build
