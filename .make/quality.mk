.PHONY: test race test-integration test-ui test-package-runner test-windows lint lint-windows lint-fix fmt vet vuln vuln-windows tidy check quality ci tools-update

test: up ## Run all Go tests, including the desktop packages
	$(GO) test -tags $(DESKTOP_TAGS) ./...

race: up ## Run all Go tests with race detection
	$(GO) test -race -shuffle=on -tags $(DESKTOP_TAGS) ./...

# The package-manager tests bound real dpkg calls to seconds, so they run apart from the loaded race pass.
test-integration: up ## Run the tests that need the real Linux package tools
	$(GO) test -tags $(GATE_TAGS) ./internal/ship/updatetransport

test-windows: up ## Compile in Docker and execute all test packages on the Windows host
	$(DOCKER_EXEC) bash scripts/build-windows-tests.sh
	bash scripts/test-windows.sh

lint: up ## Run pinned linters and go vet over CLI and desktop code
	$(TOOL) golangci-lint run --build-tags $(GATE_TAGS) ./...

# go tool builds for the target platform, so resolve the Linux binary first.
lint-windows: up ## Lint the Windows-only code from the Linux toolchain
	$(DOCKER_EXEC) bash -c 'tool=$$(go tool -modfile=tools.mod -n golangci-lint) && GOOS=windows CGO_ENABLED=0 "$$tool" run --build-tags $(WINDOWS_TAGS) ./...'

lint-fix: up ## Apply available linter fixes
	$(TOOL) golangci-lint run --fix --build-tags $(DESKTOP_TAGS) ./...

fmt: up ## Format Go source using the project configuration
	$(TOOL) golangci-lint fmt ./...

vet: up ## Run go vet
	$(GO) vet -tags $(DESKTOP_TAGS) ./...

vuln: up ## Check reachable vulnerabilities in application dependencies
	$(TOOL) govulncheck -tags $(DESKTOP_TAGS) ./...

vuln-windows: up ## Check reachable vulnerabilities in the Windows build
	$(DOCKER_EXEC) bash -c 'tool=$$(go tool -modfile=tools.mod -n govulncheck) && GOOS=windows CGO_ENABLED=0 "$$tool" -tags $(WINDOWS_TAGS) ./...'

tidy: up ## Check module metadata without modifying files
	$(GO) mod tidy -diff

test-package-runner: up ## Check installed-package process cleanup
	$(DOCKER_EXEC) python3 -m unittest discover -s tests/native -p 'test_*.py'

test-ui: up ## Check Application UI behavior without a browser or native window
	$(DOCKER_EXEC) node --test tests/ui/test_application_ui.cjs tests/ui/test_mcp_ui.cjs

check: tidy lint lint-windows race test-integration test-ui test-package-runner vuln vuln-windows ## Run the complete required quality gate

quality: check ## Alias for the complete quality gate

ci: ## Build the toolchain, fetch dependencies and run the quality gate
	$(MAKE) image-build
	$(MAKE) deps
	$(MAKE) check

tools-update: up ## Update the pinned quality tools
	$(GO) get -modfile=tools.mod -tool github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest golang.org/x/vuln/cmd/govulncheck@latest
