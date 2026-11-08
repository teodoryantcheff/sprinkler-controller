VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GO ?= go
GOBIN := $(shell $(GO) env GOPATH)/bin
export PATH := $(GOBIN):$(PATH)

.PHONY: all web build cross armv6 deploy deploy-check test vet fmt lint vuln tools hooks check run import clean

all: web build

web:
	cd web && npm ci --no-audit --no-fund && npm run build

build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/sprinkler ./cmd/sprinkler

cross:
	GOOS=linux GOARCH=arm GOARM=6 CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/sprinkler-armv6 ./cmd/sprinkler
	GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/sprinkler-armv7 ./cmd/sprinkler
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/sprinkler-arm64 ./cmd/sprinkler
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/sprinkler-amd64 ./cmd/sprinkler

armv6:
	GOOS=linux GOARCH=arm GOARM=6 CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/sprinkler-armv6 ./cmd/sprinkler

# Update the Pi (see deploy/update.sh): refuses while watering, asks
# before restarting. deploy-check only copies to /tmp and checks.
deploy: web armv6
	deploy/update.sh

deploy-check: web armv6
	deploy/update.sh -n

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

# Format everything in place: gofumpt + goimports (via golangci-lint), Prettier.
fmt:
	golangci-lint fmt ./...
	cd web && npm run format

# Check formatting and lint without changing files (the pre-commit hook runs the same).
lint:
	golangci-lint fmt --diff ./...
	golangci-lint run ./...
	cd web && npm run lint

vuln:
	govulncheck ./...

# Install the Go lint tools into $(GOBIN).
tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	$(GO) install golang.org/x/vuln/cmd/govulncheck@latest

# Use the repo's git hooks (pre-commit: format check + lint). Run once per clone.
hooks:
	git config core.hooksPath .githooks

check: lint vet test vuln
	cd web && npm run check

# Local development: import the OpenSprinkler copy, then serve with the sim driver.
import:
	$(GO) run ./cmd/sprinkler import -from testdata/os -db dev.db -force

run: build
	./dist/sprinkler serve -config deploy/dev.toml

clean:
	rm -rf dist
