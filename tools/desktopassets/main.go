// desktopassets creates the Windows icon and executable resources from icon.png.
package main

import (
	"fmt"
	"image/png"
	"os"

	"github.com/tc-hib/winres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	file, err := os.Open("internal/desktop/icon.png")
	if err != nil {
		return err
	}

	img, err := png.Decode(file)
	_ = file.Close()

	if err != nil {
		return err
	}

	icon, err := winres.NewIconFromResizedImage(img, []int{16, 32, 48, 64, 128, 256})
	if err != nil {
		return err
	}

	output, err := os.Create("bin/garminctl.ico")
	if err != nil {
		return err
	}

	err = icon.SaveICO(output)
	closeErr := output.Close()

	if err != nil {
		return err
	}

	if closeErr != nil {
		return closeErr
	}

	rs := &winres.ResourceSet{}
	// Wails loads the window and taskbar icon from resource ID 3.
	if err := rs.SetIcon(winres.ID(3), icon); err != nil {
		return err
	}

	rs.SetManifest(winres.AppManifest{
		Description: "garminctl", Compatibility: winres.Win10AndAbove,
		ExecutionLevel: winres.AsInvoker, DPIAwareness: winres.DPIPerMonitorV2,
		UseCommonControlsV6: true, LongPathAware: true,
	})

	resource, err := os.Create("cmd/garmin-desktop/resources_windows_amd64.syso")
	if err != nil {
		return err
	}

	err = rs.WriteObject(resource, winres.ArchAMD64)
	closeErr = resource.Close()

	if err != nil {
		return err
	}

	return closeErr
}
