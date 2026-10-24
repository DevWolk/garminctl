.PHONY: help

help: ## Show available Make targets
	printf 'Usage: make <target>\n\n'
	awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_.%-]+:.*## / {name=$$1; sub(/:.*$$/, "", name); printf "%-26s %s\n", name, $$2}' $(MAKEFILE_LIST) | sort
