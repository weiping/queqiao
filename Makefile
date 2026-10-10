# mbridge: one binary, cmd/mbridge, cross-compiled without cgo.

# A tag may hold $, ( or `, which the shell lines below would run: the
# version keeps only what a version is spelled with.
VERSION ?= $(or $(shell git describe --tags --always --dirty 2>/dev/null | tr -cd 'A-Za-z0-9._+-'),dev)
LDFLAGS  = -s -w -X main.version=$(VERSION)
TARGETS  = darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64
BIN     ?= mbridge

.PHONY: build install test release clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/mbridge

install:
	CGO_ENABLED=0 go install -trimpath -ldflags="$(LDFLAGS)" ./cmd/mbridge

test:
	go vet ./... && go test ./... && sh scripts/install_test.sh

# dist/mbridge-<os>-<arch>[.exe] for every target, and checksums.txt whose
# every line is "<sha256>  <file>" (shasum -c reads it as it is).
release:
	@rm -rf dist && mkdir -p dist
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=""; [ $$os = windows ] && ext=.exe; \
		echo "  $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BIN)-$$os-$$arch$$ext ./cmd/mbridge || exit 1; \
	done
	@cd dist && (shasum -a 256 $(BIN)-* 2>/dev/null || sha256sum $(BIN)-*) > checksums.txt && cat checksums.txt

clean:
	rm -rf dist $(BIN)
