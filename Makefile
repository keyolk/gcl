APP := gcl
BIN_DIR ?= $(HOME)/.local/bin
BIN := $(BIN_DIR)/$(APP)
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

GO ?= go
GOFLAGS ?=
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

.PHONY: all build install run dump fmt test lint clean deps check

all: build

build:
	$(GO) build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o ./$(APP) .

install: build
	mkdir -p $(BIN_DIR)
	cp ./$(APP) $(BIN)
	@echo "installed $(BIN)"

run:
	$(GO) run .

dump:
	$(GO) run . --dump --range day --calendar me

fmt:
	gofmt -w .

test:
	$(GO) test ./...

lint:
	$(GO) vet ./...

check: fmt test lint build

deps:
	$(GO) mod tidy

clean:
	rm -f ./$(APP)
