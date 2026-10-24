SHELL := /bin/sh

.SILENT:
.DEFAULT_GOAL := help
MAKEFLAGS += --no-print-directory

include .make/variables.mk
include .make/help.mk
include .make/docker.mk
include .make/build.mk
include .make/package.mk
include .make/quality.mk
