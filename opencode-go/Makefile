PLUGIN_NAME ?= opencode-go
VERSION ?= $(shell tr -d ' \n\r' < VERSION)
BUILD_DIR ?= dist
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
GO_LDFLAGS ?= -s -w

EXT_linux = so
EXT_darwin = dylib
EXT_windows = dll
PLUGIN_EXT = $(or $(EXT_$(GOOS)),so)
LIBRARY = $(BUILD_DIR)/$(PLUGIN_NAME).$(PLUGIN_EXT)
ARCHIVE = $(BUILD_DIR)/$(PLUGIN_NAME)_$(VERSION)_$(GOOS)_$(GOARCH).zip
CHECKSUM = $(ARCHIVE).sha256

.PHONY: test vet build package checksums clean

test:
	go test ./...

vet:
	go vet ./...

build:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -buildmode=c-shared -ldflags "$(GO_LDFLAGS)" -o $(LIBRARY) .
	rm -f $(BUILD_DIR)/$(PLUGIN_NAME).h

package: build
	go run ./scripts/package-release.go -library $(LIBRARY) -archive $(ARCHIVE) -checksum $(CHECKSUM)

checksums: package
	python3 scripts/verify_release_assets.py --archive $(ARCHIVE) --sha256 $(CHECKSUM)

clean:
	rm -rf $(BUILD_DIR)
