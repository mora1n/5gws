BINARY := 5gws
VERSION ?= dev
GOFLAGS ?=
DIST := dist

.PHONY: test build release clean

test:
	go test ./...

build:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "-s -w -X main.version=$(VERSION)" -o $(BINARY) ./cmd/5gws

release: test build
	mkdir -p $(DIST)
	tar -czf $(DIST)/$(BINARY)-linux-amd64-$(VERSION).tar.gz $(BINARY) config.example.toml rules.example.toml

clean:
	rm -rf $(BINARY) $(DIST) rendered rendered-*
