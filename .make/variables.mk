COMPOSE_FILE := docker/docker-compose.yml
GO_SERVICE := go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)
DESKTOP_TAGS := desktop,production,gtk3
export WINDOWS_SIGNING_MODE ?= none
export WINDOWS_SIGNING_DIR
export WINDOWS_SIGNING_PFX_BASE64
export WINDOWS_SIGNING_PASSWORD
export WINDOWS_TIMESTAMP_URL
export WINDOWS_TEST_CERT_THUMBPRINT

-include .make/options.local.mk

COMPOSE := docker compose -f $(COMPOSE_FILE)
DOCKER_EXEC := $(COMPOSE) exec -T $(GO_SERVICE)
DOCKER_EXEC_TTY := $(COMPOSE) exec $(GO_SERVICE)
WINDOWS_EXEC := $(COMPOSE) exec -T -e WINDOWS_SIGNING_MODE -e WINDOWS_SIGNING_DIR -e WINDOWS_SIGNING_PFX_BASE64 -e WINDOWS_SIGNING_PASSWORD -e WINDOWS_TIMESTAMP_URL $(GO_SERVICE)
GO := $(DOCKER_EXEC) go
TOOL := $(GO) tool -modfile=tools.mod

export HOST_UID ?= $(shell id -u)
export HOST_GID ?= $(shell id -g)
