BINARY := 5gws
VERSION ?= dev
GOFLAGS ?=
DIST := dist
WEB_DIST := internal/web/dist

.PHONY: test web build release clean

test: web
	go test ./...
	cd web && corepack pnpm run type-check && corepack pnpm run lint

web:
	cd web && corepack pnpm run build

build: web
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY) ./cmd/5gws

release: test build
	mkdir -p $(DIST)
	cp $(BINARY) $(DIST)/$(BINARY)-linux-amd64
	cd $(DIST) && sha256sum $(BINARY)-linux-amd64 > $(BINARY)-linux-amd64.sha256

clean:
	rm -rf $(BINARY) $(DIST) $(WEB_DIST) web/test-results web/playwright-report rendered rendered-*
