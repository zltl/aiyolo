SHELL := /bin/bash
.DEFAULT_GOAL := help

GO ?= go
APP ?= aiyolo-gateway
BUILD_DIR ?= bin
CONFIG ?= aiyolo.private.yaml
VERSION ?= dev
CLOUD_AGENT_USER ?= local-user
CLOUD_AGENT_IMAGE ?= aiyolo/local-cloud-agent:ubuntu-24.04

.PHONY: help fmt tidy test build build-aiyolo-windows build-release-artifacts publish-release-artifacts bootstrap-local-worker build-cloud-agent-image run-cloud-agent-local run clean

help:
	@printf '%s\n' \
	  'Targets:' \
	  '  make fmt               Format Go source under cmd/ and internal/' \
	  '  make tidy              Run go mod tidy' \
	  '  make test              Run default Go test suite' \
	  '  make build             Build ./cmd/gateway into bin/' \
	  '  make build-aiyolo-windows Build ./cmd/aiyolo for Windows into bin/' \
	  '  make build-release-artifacts Build gateway + wrapper release artifacts into bin/' \
	  '  make publish-release-artifacts Upload release artifacts to the configured OSS/S3 bucket' \
	  '  make bootstrap-local-worker Bootstrap the current Ubuntu host as a local worker' \
	  '  make build-cloud-agent-image Build the local Ubuntu 24.04 cloud-agent image with desktop/Chrome/DinD' \
	  '  make run-cloud-agent-local Launch a local cloud-agent container for CLOUD_AGENT_USER' \
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

build-aiyolo-windows:
	@mkdir -p $(BUILD_DIR)
	@GOOS=windows GOARCH=amd64 $(GO) build -o $(BUILD_DIR)/aiyolo.exe ./cmd/aiyolo

build-release-artifacts:
	@mkdir -p $(BUILD_DIR)
	@GOOS=windows GOARCH=amd64 $(GO) build -o $(BUILD_DIR)/aiyolo.exe ./cmd/aiyolo
	@GOOS=linux GOARCH=amd64 $(GO) build -o $(BUILD_DIR)/aiyolo-gateway-linux-amd64 ./cmd/gateway

publish-release-artifacts: build-release-artifacts
	@$(GO) run ./cmd/gateway --config $(CONFIG) publish-artifacts \
	  --version $(VERSION) \
	  --artifact $(BUILD_DIR)/aiyolo.exe=windows/aiyolo.exe \
	  --artifact $(BUILD_DIR)/aiyolo-gateway-linux-amd64=gateway/linux-amd64/aiyolo-gateway

bootstrap-local-worker:
	@./scripts/worker-bootstrap-local.sh

build-cloud-agent-image:
	@./scripts/build-cloud-agent-image-ubuntu-24.04.sh

run-cloud-agent-local:
	@AIYOLO_CLOUD_AGENT_USER_ID='$(CLOUD_AGENT_USER)' AIYOLO_CLOUD_AGENT_IMAGE='$(CLOUD_AGENT_IMAGE)' ./scripts/run-cloud-agent-local.sh

run:
	@$(GO) run ./cmd/gateway

clean:
	@rm -rf $(BUILD_DIR)