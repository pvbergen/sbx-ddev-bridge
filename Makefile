.PHONY: all host clients test release package clean

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || date -u +%Y%m%d.%H%M)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOBUILD := CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)"
DIST    := ddev-bridge-dist
PKG     := dist/ddev-bridge.tar.gz
# Files that make up the package (plus $(DIST)/ with the binaries).
PKGFILES := install.sh commands README.md LICENSE docs

all: test host clients

host:            ## dev build of the bridge for this machine
	$(GOBUILD) -o dist/ ./cmd/ddev-bridge

clients:         ## dev build of the sandbox clients (sandboxes are Linux)
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o dist/linux-amd64/ ./cmd/ddev-bridge-client
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o dist/linux-arm64/ ./cmd/ddev-bridge-client

test:
	go vet ./...
	go test -race -count=1 ./...

release: test    ## build the binaries into ddev-bridge-dist/ (gitignored; CI does this)
	@rm -rf $(DIST)
	@for t in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
	  os=$${t%/*}; arch=$${t#*/}; \
	  echo "host   $$os-$$arch"; \
	  GOOS=$$os GOARCH=$$arch $(GOBUILD) -o $(DIST)/host/$$os-$$arch/ddev-bridge ./cmd/ddev-bridge || exit 1; \
	done
	@for arch in amd64 arm64; do \
	  echo "client linux-$$arch"; \
	  GOOS=linux GOARCH=$$arch $(GOBUILD) -o $(DIST)/client/linux-$$arch/ddev-bridge-client ./cmd/ddev-bridge-client || exit 1; \
	done
	@du -sh $(DIST)

package: release ## installable tarball: dist/ddev-bridge.tar.gz (the GitHub release asset)
	@mkdir -p dist
	tar -czf $(PKG) --transform 's,^,ddev-bridge/,' $(PKGFILES) $(DIST)
	@ls -lh $(PKG)

clean:           ## remove all build output
	rm -rf dist $(DIST)
