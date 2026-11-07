VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GO ?= go

.PHONY: all web build cross test vet check run import clean

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

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

check: vet test
	cd web && npm run check

# Local development: import the OpenSprinkler copy, then serve with the sim driver.
import:
	$(GO) run ./cmd/sprinkler import -from testdata/os -db dev.db -force

run: build
	./dist/sprinkler serve -config deploy/dev.toml

clean:
	rm -rf dist
