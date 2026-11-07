SHELL := /bin/bash

BINARY           := community_service
GO               ?= go
TEST_FLAGS       := -race -count=1 -parallel 4
COVERAGE_FILE    := coverage.out
COVERAGE_MIN     := 80

.PHONY: all build run run-check test cover cover-check vet fmt tidy clean docker-build

all: build

## build: compile the service binary
build:
	$(GO) build -trimpath -o bin/$(BINARY) ./cmd/server

## run: start the service with the environment of the current shell
run:
	$(GO) run ./cmd/server

## test: the documented validation command
test:
	$(GO) test ./... $(TEST_FLAGS)

## cover: run the suite with a coverage profile and print the total
cover:
	$(GO) test ./... $(TEST_FLAGS) -coverprofile=$(COVERAGE_FILE) -covermode=atomic
	@$(GO) tool cover -func=$(COVERAGE_FILE) | tail -1

## cover-check: fail when total coverage drops below COVERAGE_MIN
cover-check: cover
	@total=$$($(GO) tool cover -func=$(COVERAGE_FILE) | tail -1 | awk '{print $$3}' | tr -d '%'); \
	echo "total coverage: $$total% (minimum $(COVERAGE_MIN)%)"; \
	awk "BEGIN { exit !($$total >= $(COVERAGE_MIN)) }" || { echo "coverage below $(COVERAGE_MIN)%"; exit 1; }

## vet: static checks over every package
vet:
	$(GO) vet ./...

## fmt: format the tree
fmt:
	gofmt -l -w cmd internal tests

## tidy: reconcile go.mod / go.sum
tidy:
	$(GO) mod tidy

## docker-build: build the container image
docker-build:
	docker build -t $(BINARY):local .

## clean: remove build and coverage artefacts
clean:
	rm -rf bin $(COVERAGE_FILE)
