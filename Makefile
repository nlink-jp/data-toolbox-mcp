MODULE  := github.com/nlink-jp/data-toolbox-mcp
BINARY  := data-toolbox-mcp
BIN_DIR := dist

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X $(MODULE)/cmd.Version=$(VERSION)"

# macOS Developer ID signing / notarization (see CONVENTIONS.md §Code
# Signing). Defaults match any Developer ID Application cert in the
# keychain and the org-standard notary profile. Builds without these
# fall back to ad-hoc / un-notarized with a one-line warning.
CODESIGN_IDENTITY ?= Developer ID Application
NOTARY_PROFILE    ?= nlink-jp-notary

PLATFORMS := \
	darwin/amd64 \
	darwin/arm64 \
	linux/amd64 \
	linux/arm64

.PHONY: build build-all package test clean runtime-image help

## build: Build binary for the current OS/Arch → ./dist/data-toolbox-mcp
build:
	@mkdir -p $(BIN_DIR)
	go build $(LDFLAGS) -o $(BIN_DIR)/$(BINARY) .
	@scripts/codesign-darwin.sh $(BIN_DIR)/$(BINARY) "$(CODESIGN_IDENTITY)"

## build-all: Cross-compile and codesign each darwin build
build-all:
	@mkdir -p $(BIN_DIR)
	$(foreach platform,$(PLATFORMS),$(call build_platform,$(platform)))

define build_platform
	$(eval OS   := $(word 1,$(subst /, ,$(1))))
	$(eval ARCH := $(word 2,$(subst /, ,$(1))))
	$(eval OUT  := $(BIN_DIR)/$(BINARY)-$(OS)-$(ARCH))
	@echo "Building $(OUT)..."
	GOOS=$(OS) GOARCH=$(ARCH) go build $(LDFLAGS) -o $(OUT) .
	@scripts/codesign-darwin.sh $(OUT) "$(CODESIGN_IDENTITY)"

endef

## package: Cross-compile, codesign, zip, and notarize darwin builds
package: build-all
	$(foreach platform,$(PLATFORMS), \
		$(eval OS   := $(word 1,$(subst /, ,$(platform)))) \
		$(eval ARCH := $(word 2,$(subst /, ,$(platform)))) \
		$(eval BIN  := $(BIN_DIR)/$(BINARY)-$(OS)-$(ARCH)) \
		$(eval ZIP  := $(BIN_DIR)/$(BINARY)-$(VERSION)-$(OS)-$(ARCH).zip) \
		$(eval STAGE := $(BIN_DIR)/_pkg-$(OS)-$(ARCH)) \
		rm -rf $(STAGE) && mkdir -p $(STAGE) ; \
		cp $(BIN) $(STAGE)/$(BINARY) ; \
		zip -j $(ZIP) $(STAGE)/$(BINARY) ; \
		rm -rf $(STAGE) ;)
	@scripts/notarize-darwin.sh $(BIN_DIR)/$(BINARY)-$(VERSION)-darwin-amd64.zip "$(NOTARY_PROFILE)"
	@scripts/notarize-darwin.sh $(BIN_DIR)/$(BINARY)-$(VERSION)-darwin-arm64.zip "$(NOTARY_PROFILE)"

## test: Run all unit tests
test:
	go test ./...

## clean: Remove build artifacts
clean:
	rm -rf $(BIN_DIR)

## runtime-image: Convenience wrapper around `data-toolbox-mcp build-runtime` (ADR-0005)
runtime-image: build
	$(BIN_DIR)/$(BINARY) build-runtime

## help: Show available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
