GO ?= go
PYTHON ?= python3
DIST ?= dist
SERVER_VERSION ?= 0.1.0-dev
VERSION_HASH ?= $(shell git rev-parse --verify HEAD 2>/dev/null || printf unknown)
MODULE := github.com/komari-monitor/komari
LDFLAGS := -s -w -X $(MODULE)/utils.ServerVersion=$(SERVER_VERSION) -X $(MODULE)/utils.VersionHash=$(VERSION_HASH)

.PHONY: build check notices clean

build:
	mkdir -p "$(DIST)"
	CGO_ENABLED=1 $(GO) build -trimpath -ldflags='$(LDFLAGS)' -o "$(DIST)/shellbeam-server" .

check:
	CGO_ENABLED=1 $(GO) test ./...
	CGO_ENABLED=1 $(GO) vet ./...

notices:
	$(GO) mod download all
	rm -rf "$(DIST)/THIRD_PARTY_NOTICES"
	$(PYTHON) .github/scripts/collect-notices.py --output "$(DIST)/THIRD_PARTY_NOTICES"

clean:
	rm -rf "$(DIST)"
