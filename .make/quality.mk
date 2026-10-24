.PHONY: test race test-windows lint lint-fix fmt vet vuln tidy check quality ci tools-update

test: up ## Run all Go tests, including the desktop packages
	$(GO) test -tags $(DESKTOP_TAGS) ./...

race: up ## Run all Go tests with race detection
	$(GO) test -race -shuffle=on -tags $(DESKTOP_TAGS) ./...

test-windows: up ## Compile in Docker and execute all test packages on the Windows host
	$(DOCKER_EXEC) bash scripts/build-windows-tests.sh
	bash scripts/test-windows.sh

lint: up ## Run pinned linters and go vet over CLI and desktop code
	$(TOOL) golangci-lint run --build-tags $(DESKTOP_TAGS) ./...

lint-fix: up ## Apply available linter fixes
	$(TOOL) golangci-lint run --fix --build-tags $(DESKTOP_TAGS) ./...

fmt: up ## Format Go source using the project configuration
	$(TOOL) golangci-lint fmt ./...

vet: up ## Run go vet
	$(GO) vet -tags $(DESKTOP_TAGS) ./...

vuln: up ## Check reachable vulnerabilities in application dependencies
	$(TOOL) govulncheck -tags $(DESKTOP_TAGS) ./...

tidy: up ## Check module metadata without modifying files
	$(GO) mod tidy -diff

check: tidy lint race vuln ## Run the complete required quality gate

quality: check ## Alias for the complete quality gate

ci: ## Build the toolchain, fetch dependencies and run the quality gate
	$(MAKE) image-build
	$(MAKE) deps
	$(MAKE) check

tools-update: up ## Update the pinned quality tools
	$(GO) get -modfile=tools.mod -tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest golang.org/x/vuln/cmd/govulncheck@latest
	$(GO) mod tidy -modfile=tools.mod
