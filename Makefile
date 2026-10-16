# Relevo build entry points. See README.md.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GO      ?= go
export CGO_ENABLED ?= 0

.PHONY: all web host test test-race contracts e2e cross clean

all: web host

web:
	rm -rf web/controller/dist/assets web/controller/dist/index.html
	cd web/controller && npm ci --no-audit --no-fund && npm run build

host:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/relevo-host ./cmd/relevo-host

test:
	$(GO) vet ./...
	$(GO) test ./...

test-race:
	CGO_ENABLED=1 $(GO) test -race ./...

contracts:
	cd tools/contracts && npm ci --no-audit --no-fund && npm run check

e2e: all
	cd web/controller && npm run e2e

# Cross-compilation proves the code COMPILES for each target; it is not a
# functional test (docs/platform-support.md).
cross:
	@for t in linux/amd64 linux/arm64 windows/amd64 darwin/amd64 darwin/arm64 freebsd/amd64; do \
	  os=$${t%/*}; arch=$${t#*/}; ext=; [ $$os = windows ] && ext=.exe; \
	  echo "== $$t"; \
	  GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/$$os-$$arch/relevo-host$$ext ./cmd/relevo-host || exit 1; \
	done

clean:
	rm -rf bin dist
