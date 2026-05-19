# Thin wrapper that mirrors the README's `make cli` / `make cli-release`
# examples (instead of asking users to remember
# `go build -o bin/everyapi .`). All paths are relative to this
# Makefile's directory, so it stays valid whether the source tree
# is checked out standalone or vendored under a larger workspace.

CLI_VERSION ?= dev
CLI_COMMIT  ?= $$(git rev-parse --short HEAD 2>/dev/null || echo unknown)

# Resolve the current module path at make-time so the -X ldflag
# target tracks whatever go.mod actually declares. Hardcoding the
# path would silently break the build whenever the module is moved
# or vendored under a different prefix: the ldflag symbol wouldn't
# match, and `everyapi version` would fall back to the "dev"
# placeholder without raising an error.
#
# Recursive assignment (=, not :=) so the shell-out fires lazily —
# `make help` / `make clean` don't pay the cost (or fail loudly)
# when go isn't installed. Targets that actually need MODULE depend
# on the `_require-module` guard below, which surfaces the empty
# case as a fail-fast error rather than silently producing a
# binary whose ldflag path doesn't match the real symbol.
MODULE = $(shell go list -m 2>/dev/null)

LDFLAGS = -s -w \
  -X $(MODULE)/internal/version.Version=$(CLI_VERSION) \
  -X $(MODULE)/internal/version.Commit=$(CLI_COMMIT)

.PHONY: cli test fmt lint cli-release clean help _require-module

help:
	@echo "Targets:"
	@echo "  make cli           Build ./bin/everyapi for the host platform"
	@echo "  make test          Run the full unit test suite"
	@echo "  make fmt           gofmt -w ."
	@echo "  make lint          go vet ./..."
	@echo "  make cli-release   Cross-compile 5 platform binaries into ./dist"
	@echo "  make clean         Remove bin/ + dist/"

# Fail fast if `go list -m` returned nothing. Without this, the -X
# ldflag silently becomes "-X /internal/version.Version=..." with a
# leading slash, the symbol doesn't match, and the resulting binary
# reports "dev" — a confusing-to-debug failure mode.
_require-module:
	@if [ -z "$(MODULE)" ]; then \
	  echo "error: could not resolve module path via 'go list -m'." >&2; \
	  echo "       Run this target from the module root (the directory containing go.mod) with go installed." >&2; \
	  exit 1; \
	fi

cli: _require-module
	@mkdir -p bin
	@go build -ldflags "$(LDFLAGS)" -o bin/everyapi .
	@echo "Built bin/everyapi ($(CLI_VERSION) @ $(CLI_COMMIT))"

test:
	@go test ./...

fmt:
	@gofmt -w .

lint:
	@go vet ./...

cli-release: _require-module
	@echo "Cross-compiling everyapi $(CLI_VERSION) @ $(CLI_COMMIT) ..."
	@mkdir -p dist
	@for target in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64; do \
	  GOOS=$${target%_*}; GOARCH=$${target#*_}; \
	  EXT=""; \
	  [ "$$GOOS" = "windows" ] && EXT=".exe"; \
	  OUT="dist/everyapi_$${target}$${EXT}"; \
	  echo "  $$OUT"; \
	  CGO_ENABLED=0 GOOS=$$GOOS GOARCH=$$GOARCH \
	    go build -ldflags "$(LDFLAGS)" -o "$$OUT" .; \
	done
	@ls -la dist

clean:
	@rm -rf bin dist
