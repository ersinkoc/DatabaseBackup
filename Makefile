.PHONY: help build test lint fmt vet vuln bench integration e2e ui release release-all provenance sbom verify-sbom sign-release verify-signatures verify-release archive-release-evidence release-rehearsal smoke-release production-check clean check

BIN := bin/kronos
GO ?= go
GOFMT ?= gofmt
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
GOFILES := $(shell find . -name '*.go' -not -path './.git/*' -not -path './.tools/*' -not -path './bin/*')
LDFLAGS := -s -w \
	-X github.com/kronos/kronos/internal/buildinfo.Version=$(VERSION) \
	-X github.com/kronos/kronos/internal/buildinfo.Commit=$(COMMIT) \
	-X github.com/kronos/kronos/internal/buildinfo.BuildDate=$(BUILD_DATE)

help:
	@printf '%s\n' \
		'Targets:' \
		'  build        Build ./bin/kronos' \
		'  test         Run unit tests' \
		'  lint         Run staticcheck when installed' \
		'  fmt          Format Go files' \
		'  vet          Run go vet' \
		'  vuln         Run govulncheck when installed' \
		'  bench        Run benchmarks' \
		'  integration  Run integration tests' \
		'  e2e          Run end-to-end tests' \
		'  ui           Build the embedded WebUI' \
		'  release      Build a stamped release binary and checksum' \
		'  release-all  Build stamped release binaries and checksums for common platforms' \
		'  provenance   Write release artifact provenance JSON' \
		'  sbom         Write Go module SBOM JSON' \
		'  verify-sbom  Verify release SBOM module coverage and vulnerabilities' \
		'  sign-release Sign release binaries and metadata with cosign keyless signing' \
		'  verify-signatures Verify cosign keyless release signatures' \
		'  verify-release Verify release binaries, checksums, and metadata' \
		'  archive-release-evidence Archive checksum/signature/attestation verification logs' \
		'  release-rehearsal Download a tagged release and archive verification evidence' \
		'  smoke-release Execute the host release artifact and completion output' \
		'  production-check Run the release-readiness gate without requiring make recursion' \
		'  clean        Remove generated artifacts' \
		'  check        Run fmt check, vet, lint, vuln, tests, build, and script checks'

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/kronos

test:
	$(GO) test ./...

lint:
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; else echo 'staticcheck not installed; skipping'; fi

fmt:
	$(GOFMT) -w -s $(GOFILES)

vet:
	$(GO) vet ./...

vuln:
	@if command -v govulncheck >/dev/null 2>&1; then govulncheck ./...; else echo 'govulncheck not installed; skipping'; fi

bench:
	@mkdir -p bench
	$(GO) test -bench=. -run='^$$' ./... | tee bench/bench.out

integration:
	$(GO) test -tags=integration ./...

e2e:
	$(GO) test -tags=e2e ./...

ui:
	./web/build.sh

release:
	GO=$(GO) VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_DATE="$(BUILD_DATE)" ./scripts/build.sh

release-all:
	GO=$(GO) VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_DATE="$(BUILD_DATE)" ./scripts/release.sh

provenance:
	VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_DATE="$(BUILD_DATE)" ./scripts/provenance.sh

sbom:
	GO=$(GO) VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_DATE="$(BUILD_DATE)" ./scripts/sbom.sh

verify-sbom:
	GO=$(GO) ./scripts/verify-sbom.sh bin

sign-release:
	./scripts/sign-release.sh bin

verify-signatures:
	./scripts/verify-signatures.sh bin

verify-release:
	./scripts/verify-release.sh bin

archive-release-evidence:
	./scripts/archive-release-evidence.sh bin release-evidence

release-rehearsal:
	@if [ -z "$(TAG)" ]; then echo 'TAG is required, for example: make release-rehearsal TAG=v0.1.0'; exit 2; fi
	./scripts/release-rehearsal.sh "$(TAG)" "release-evidence/$(TAG)"

smoke-release:
	GO=$(GO) ./scripts/smoke-release.sh bin

production-check:
	GO=$(GO) GOFMT=$(GOFMT) BIN=$(BIN) VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_DATE="$(BUILD_DATE)" ./scripts/production-check.sh

clean:
	rm -rf bin bench/bench.out

check:
	@test -z "$$($(GOFMT) -l -s $(GOFILES))" || ($(GOFMT) -l -s $(GOFILES) && echo 'gofmt required; run make fmt' && exit 1)
	$(GO) vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then staticcheck ./...; else echo 'staticcheck not installed; skipping'; fi
	@if command -v govulncheck >/dev/null 2>&1; then govulncheck ./...; else echo 'govulncheck not installed; skipping'; fi
	@cc="$$(CGO_ENABLED=1 $(GO) env CC)"; \
	if command -v "$$cc" >/dev/null 2>&1; then \
		CGO_ENABLED=1 $(GO) test -race ./...; \
	else \
		echo 'cgo C compiler not found; running non-race tests'; \
		$(GO) test ./...; \
	fi
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/kronos
	sh -n scripts/build.sh
	sh -n scripts/release.sh
	sh -n scripts/provenance.sh
	sh -n scripts/archive-release-evidence.sh
	sh -n scripts/release-rehearsal.sh
	sh -n scripts/sign-release.sh
	sh -n scripts/sbom.sh
	sh -n scripts/verify-sbom.sh
	sh -n scripts/verify-signatures.sh
	sh -n scripts/verify-release.sh
	sh -n scripts/smoke-release.sh
	sh -n web/build.sh
	$(BIN) completion bash | bash -n
