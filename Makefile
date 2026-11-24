BINARY  := commhub
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

PREFIX           ?= $(HOME)/.local
BINDIR           ?= $(PREFIX)/bin
SYSTEMD_USER_DIR ?= $(HOME)/.config/systemd/user

.PHONY: all build test lint fmt deps deps-update run migrate install uninstall clean

all: build

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/commhub

test:
	go test ./...

lint:
	golangci-lint run --allow-parallel-runners

fmt:
	gofmt -w cmd pkg

deps:
	go mod tidy
	go mod vendor

deps-update:
	go get -u ./...
	$(MAKE) deps

run: build
	./bin/$(BINARY) serve

migrate:
	go run ./cmd/commhub migrate

install: build
	install -Dm755 bin/$(BINARY) $(BINDIR)/$(BINARY)
	install -Dm644 contrib/systemd/commhub.service $(SYSTEMD_USER_DIR)/commhub.service

uninstall:
	rm -f $(BINDIR)/$(BINARY) $(SYSTEMD_USER_DIR)/commhub.service

clean:
	rm -rf bin
