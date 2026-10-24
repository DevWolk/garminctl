.PHONY: cache-init image-build up bash ps stop down deps

cache-init: ## Create writable build caches owned by the host user
	mkdir -p .cache/go-build .cache/go-mod bin dist

image-build: ## Build the Go, WebKit and installer toolchain image
	$(COMPOSE) build --pull

up: cache-init ## Start the shared development container
	$(COMPOSE) up --detach --remove-orphans

bash: up ## Open a shell in the build container
	$(DOCKER_EXEC_TTY) bash

ps: ## Show project containers
	$(COMPOSE) ps --all

stop: ## Stop the development container
	$(COMPOSE) stop

down: ## Remove the development container, keeping local caches
	$(COMPOSE) down --remove-orphans

deps: up ## Download pinned application and quality-tool dependencies
	$(GO) mod download
	$(GO) mod download -modfile=tools.mod
