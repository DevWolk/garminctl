.PHONY: all build build-linux-usb build-windows build-desktop build-desktop-windows install update clean

all: build build-windows build-desktop build-desktop-windows ## Build CLI and desktop binaries for Linux and Windows

build: up ## Build the Linux/WSL command-line binary
	$(DOCKER_EXEC) env CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/garminctl ./cmd/garminctl

build-linux-usb: up ## Build the Linux package's CLI with native libusb support
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/garminctl-linux ./cmd/garminctl

build-windows: up ## Build the Windows command-line binary
	$(DOCKER_EXEC) env GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/garminctl.exe ./cmd/garminctl

build-desktop: up ## Build the native Linux application
	$(GO) build -tags $(DESKTOP_TAGS) -ldflags "$(LDFLAGS)" -o bin/garmin-desktop ./cmd/garmin-desktop

build-desktop-windows: assets ## Build the native Windows application
	$(DOCKER_EXEC) env GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags desktop,production -ldflags "-H windowsgui $(LDFLAGS)" -o bin/garmin-desktop.exe ./cmd/garmin-desktop

install: all ## Rebuild and update the development server and Windows autostart copy
	./scripts/install.sh

update: ## Fast-forward the current branch and refresh the development install
	git pull --ff-only
	$(MAKE) install

clean: ## Remove local build outputs
	rm -f bin/garminctl bin/garminctl-linux bin/garminctl.exe bin/garmin-desktop bin/garmin-desktop.exe
