.DEFAULT_GOAL := build

BUILD_CONCURRENCY ?= 2
PROVIDER ?= native

BIN := _output/bin
GO_BUILD_ENV = GOMAXPROCS=$(BUILD_CONCURRENCY) GOFLAGS="$(strip $(GOFLAGS) -p=$(BUILD_CONCURRENCY))"
LDFLAGS := -X k8s.io/component-base/version.gitVersion=v1.37.1 -X k8s.io/component-base/version.gitMajor=1 -X k8s.io/component-base/version.gitMinor=37

.PHONY: build build-darwin-helper build-worker check generate help run test verify

build: build-worker build-darwin-helper
	$(GO_BUILD_ENV) go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN)/ble-controlplane ./cmd/ble-controlplane

build-darwin-helper:
ifeq ($(shell uname -s),Darwin)
	mkdir -p $(BIN)
	xcrun --sdk macosx swiftc -O -framework CoreBluetooth -module-cache-path _output/swift-cache \
		-Xlinker -sectcreate -Xlinker __TEXT -Xlinker __info_plist -Xlinker cmd/ble-controlplane-ble-darwin/Info.plist \
		-o $(BIN)/ble-controlplane-ble-darwin cmd/ble-controlplane-ble-darwin/main.swift
endif

build-worker: node_modules/.package-lock.json
	npm run build

check:
	$(MAKE) verify
	$(MAKE) test

generate:
	$(GO_BUILD_ENV) bash hack/update-codegen.sh

help:
	@printf '%s\n' \
		'make build                 Build the daemon, dashboard worker, and macOS scanner' \
		'make run                   Start the control plane with the host scanner' \
		'make run PROVIDER=replay   Start the replay demo' \
		'make check                 Verify the source and run the tests' \
		'make generate              Regenerate Kubernetes API helpers and clients' \
		'make test                  Run the Go tests and the replay integration test' \
		'make verify                Check formatting, generation, Go, and TypeScript'

run: build
	exec ./$(BIN)/ble-controlplane --provider "$(PROVIDER)"

node_modules/.package-lock.json: package.json package-lock.json
	npm ci --no-audit --no-fund

test: build-worker
	BLE_WORKER_PATH="$(CURDIR)/$(BIN)/ble-controlplane-ui.cjs" $(GO_BUILD_ENV) go test -count=1 ./...

verify: node_modules/.package-lock.json
	npm run check
	bash hack/verify-gofmt.sh
	$(GO_BUILD_ENV) go mod tidy -diff
	$(GO_BUILD_ENV) go vet ./...
	$(GO_BUILD_ENV) bash hack/verify-codegen.sh
