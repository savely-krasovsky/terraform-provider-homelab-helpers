GO ?= go
VERSION ?= dev
BUILD_DIR ?= bin

export CGO_ENABLED = 0
export GOWORK = off

LDFLAGS = -X main.version=$(patsubst v%,%,$(VERSION))

.PHONY: build install fmt lint test testacc vet generate check

build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/terraform-provider-homelab-helpers .

install:
	$(GO) install -trimpath -ldflags '$(LDFLAGS)' .

fmt:
	gofmt -w main.go internal tools/tools.go

lint:
	golangci-lint run

vet:
	$(GO) vet ./...

test:
	$(GO) test -timeout 2m ./...

testacc:
	TF_ACC=1 TF_ACC_PROVIDER_NAMESPACE=savely-krasovsky $(GO) test -timeout 10m -run TestAcc ./internal/provider

generate:
	cd tools && $(GO) generate -tags generate ./...

check: test vet lint
