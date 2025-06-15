PROJECT_DIR := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
BUILD_DIR := $(PROJECT_DIR)/build
WORKING_DIR := $(PROJECT_DIR)/working
MODULE_NAME := $(shell go list -m)

# Licensing targets
LICENSE_DIR = $(BUILD_DIR)/licenses
GO_DEPENDENCIES_LICENSE_DIR = $(LICENSE_DIR)/go-dependencies
BUILT_LICENSES := $(LICENSE_DIR)/LICENSE $(GO_DEPENDENCIES_LICENSE_DIR)

$(BUILT_LICENSES) &: go.mod LICENSE
	@mkdir -p "$(LICENSE_DIR)"
	@cp LICENSE "$(LICENSE_DIR)"
	@rm -rf "$(GO_DEPENDENCIES_LICENSE_DIR)"
	@go run github.com/google/go-licenses@latest save ./... --save_path="$(GO_DEPENDENCIES_LICENSE_DIR)" --ignore "$(MODULE_NAME)"

PHONY += licenses
ALL_BUILDERS += licenses
licenses: $(BUILT_LICENSES)

PHONY += check-licenses
check-licenses:
	@go run github.com/google/go-licenses@latest report ./...

.PHONY: $(PHONY)

# Build targets
VERSION = 0.0.1-dev
CONTAINER_REGISTRY = ghcr.io/soliddowant
PUSH_ALL ?= false

## Binary build targets
BINARY_DIR = $(BUILD_DIR)/binaries
BINARY_PLATFORMS = linux/amd64 linux/arm64
BINARY_NAME = pod-webhook-tracker
GO_SOURCE_FILES := $(shell find . \( -name '*.go' ! -name '*_test.go' ! -name '*_mock*.go' ! -path './pkg/testhelpers/*' ! -path '*/fake/*' \))
GO_CONSTANTS := Version=$(VERSION)
GO_LDFLAGS := $(GO_CONSTANTS:%=-X $(MODULE_NAME)/main.%) -s -w

LOCALOS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
LOCALARCH := $(shell uname -m | sed 's/x86_64/amd64/')
LOCAL_BINARY_PATH := $(BINARY_DIR)/$(LOCALOS)/$(LOCALARCH)/$(BINARY_NAME)

$(BINARY_DIR)/%/$(BINARY_NAME): $(GO_SOURCE_FILES)
	@mkdir -p "$(@D)"
	@GOOS="$(word 1,$(subst /, ,$*))" GOARCH="$(word 2,$(subst /, ,$*))" go build -ldflags="$(GO_LDFLAGS)" -o "$@" .

PHONY += binary
LOCAL_BUILDERS += binary
binary: $(LOCAL_BINARY_PATH)

PHONY += binary-all
ALL_BUILDERS += binary-all
binary-all: $(BINARY_PLATFORMS:%=$(BINARY_DIR)/%/$(BINARY_NAME))

## Tarball build targets
TARBALL_DIR = $(BUILD_DIR)/tarballs
LOCAL_TARBALL_PATH := $(TARBALL_DIR)/$(LOCALOS)/$(LOCALARCH)/$(BINARY_NAME).tar.gz

$(TARBALL_DIR)/%/$(BINARY_NAME).tar.gz: $(BINARY_DIR)/%/$(BINARY_NAME) licenses
	@mkdir -p "$(@D)"
	@tar -czf "$@" -C "$(BINARY_DIR)/$*" "$(BINARY_NAME)" -C "$(dir $(LICENSE_DIR))" "$(notdir $(LICENSE_DIR))"

PHONY += tarball
LOCAL_BUILDERS += tarball
tarball: $(LOCAL_TARBALL_PATH)

PHONY += tarball-all
ALL_BUILDERS += tarball-all
tarball-all: $(BINARY_PLATFORMS:%=$(TARBALL_DIR)/%/$(BINARY_NAME).tar.gz)

## Container build targets
DEBIAN_IMAGE_VERSION = 12.9-slim

CONTAINER_IMAGE_TAG = $(CONTAINER_REGISTRY)/$(BINARY_NAME):$(VERSION)
CONTAINER_BUILD_LABEL_VARS = org.opencontainers.image.source=https://github.com/solidDoWant/pod-webhook-tracker org.opencontainers.image.licenses=AGPL-3.0
CONTAINER_BUILD_LABELS := $(foreach var,$(CONTAINER_BUILD_LABEL_VARS),--label $(var))
CONTAINER_BUILD_ARG_VARS = DEBIAN_IMAGE_VERSION
CONTAINER_BUILD_ARGS := $(foreach var,$(CONTAINER_BUILD_ARG_VARS),--build-arg $(var)=$($(var)))
CONTAINER_PLATFORMS := $(BINARY_PLATFORMS)

PHONY += container-image
LOCAL_BUILDERS += container-image
container-image: binary licenses
	@docker buildx build --platform linux/$(LOCALARCH) -t $(CONTAINER_IMAGE_TAG) --load $(CONTAINER_BUILD_ARGS) $(CONTAINER_BUILD_LABELS) .

CONTAINER_MANIFEST_PUSH ?= $(PUSH_ALL)

PHONY += container-manifest
ALL_BUILDERS += container-manifest
container-manifest: PUSH_ARG = $(if $(findstring t,$(CONTAINER_MANIFEST_PUSH)),--push)
container-manifest: $(CONTAINER_PLATFORMS:%=$(BINARY_DIR)/%/$(BINARY_NAME)) licenses
	@docker buildx build $(CONTAINER_PLATFORMS:%=--platform %) $(PUSH_ARG) -t $(CONTAINER_IMAGE_TAG) $(CONTAINER_BUILD_ARGS) $(CONTAINER_BUILD_LABELS) .

## Build all targets
PHONY += build
.DEFAULT_GOAL:= build
build: $(LOCAL_BUILDERS)

PHONY += build-all
build-all: $(ALL_BUILDERS)

# Release targets
RELEASE_DIR = $(BUILD_DIR)/releases/$(VERSION)

PHONY += release
release: TAG = v$(VERSION)
release: CP_CMDS = $(foreach PLATFORM,$(BINARY_PLATFORMS),cp $(TARBALL_DIR)/$(PLATFORM)/$(BINARY_NAME).tar.gz $(RELEASE_DIR)/$(BINARY_NAME)-$(VERSION)-$(subst /,-,$(PLATFORM)).tar.gz &&) true
release: SAFETY_PREFIX = $(if $(findstring t,$(PUSH_ALL)),,echo)
release: build-all
	@mkdir -p $(RELEASE_DIR)
	@gh auth status
	@$(CP_CMDS)
	@$(SAFETY_PREFIX) git tag -a $(TAG) -m "Release $(TAG)"
	@$(SAFETY_PREFIX) git push origin
	@$(SAFETY_PREFIX) git push origin --tags
	@$(SAFETY_PREFIX) gh release create $(TAG) --generate-notes --latest --verify-tag "$(RELEASE_DIR)"/*

# Cleaning targets
PHONY += clean
clean:
	@rm -rf $(BUILD_DIR) $(WORKING_DIR)
	@docker image rm -f $(CONTAINER_IMAGE_TAG) 2> /dev/null > /dev/null || true

clean-all: clean
