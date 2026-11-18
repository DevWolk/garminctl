.PHONY: assets package package-linux package-windows

assets: up ## Generate native application icons and Windows resources
	$(DOCKER_EXEC) rsvg-convert -w 256 -h 256 -o internal/ship/desktop/icon.png internal/ship/web/ui/icon.svg
	$(GO) run ./tools/desktopassets

package: package-linux package-windows ## Build the Windows installer and Linux DEB/RPM packages

package-linux: build-linux-usb build-desktop ## Build Linux installation packages in dist/
	$(DOCKER_EXEC) bash scripts/package-linux.sh "$(VERSION)"

package-windows: build-windows build-desktop-windows ## Build the Windows setup executable in dist/
	$(WINDOWS_EXEC) bash scripts/package-windows.sh "$(VERSION)"
