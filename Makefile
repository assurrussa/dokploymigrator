GOENV := GOTOOLCHAIN=local GOCACHE=$(CURDIR)/tmp/gocache GOPATH=$(CURDIR)/tmp/gopath
LINTENV := $(GOENV) GOLANGCI_LINT_CACHE=$(CURDIR)/tmp/golangci-lint

.PHONY: test build run race vet lint e2e-db verify

build:
	$(GOENV) go build ./cmd/dokploy-migrator

run:
	$(GOENV) go run ./cmd/dokploy-migrator serve

test:
	$(GOENV) go test ./...

race:
	$(GOENV) go test -race ./...

vet:
	$(GOENV) go vet ./...

lint:
	$(LINTENV) golangci-lint run ./...

e2e-db:
	./scripts/e2e-dokploy-db.sh

verify: test race vet lint
