SHELL := /bin/bash
.DEFAULT_GOAL := help

.PHONY: help
## help| Makefile| Prints this help message
help:
	@echo "Usage:"
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s '|' |  sed -e 's/^/ /'

.PHONY: clean
## clean| Go| Clean
clean:
	go clean

.PHONY: build
## build| Go| Build executable binary
build:
	go build -trimpath -o readme-generator-for-helm

.PHONY: release
## release| Go| Build optimized binary matching the CI release build
release:
	go build -trimpath -ldflags '-s -w' -tags netgo -o readme-generator-for-helm

.PHONY: test
## test| Go| Run tests
test:
	go test ./...

.PHONY: lint
## lint| Go| Run linter
lint:
	golangci-lint run ./...

.PHONY: format
## format| Go| Run formatter
format:
	gofmt -s -w -e .

.PHONY: tidy
## tidy| Go| Add missing and remove unused modules
tidy:
	go mod tidy
