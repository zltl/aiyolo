SHELL := /bin/bash
.DEFAULT_GOAL := help

GO ?= go
APP ?= aiyolo-gateway
BUILD_DIR ?= bin

.PHONY: help fmt tidy test build run clean

help:
	@printf '%s\n' \
	  'Targets:' \
	  '  make fmt               Format Go source under cmd/ and internal/' \
	  '  make tidy              Run go mod tidy' \
	  '  make test              Run default Go test suite' \
	  '  make build             Build ./cmd/gateway into bin/' \
	  '  make run               Run gateway' \
	  '  make clean             Remove build output'

fmt:
	@$(GO)fmt -w cmd internal

tidy:
	@$(GO) mod tidy

test:
	@$(GO) test ./...

build:
	@mkdir -p $(BUILD_DIR)
	@$(GO) build -o $(BUILD_DIR)/$(APP) ./cmd/gateway

run:
	@$(GO) run ./cmd/gateway

clean:
	@rm -rf $(BUILD_DIR)