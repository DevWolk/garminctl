package mtp

import (
	"fmt"

	"github.com/DevWolk/garminctl/internal/device"
)

func selectDevice(candidates []device.Info, id string) (device.Info, error) {
	if id != "" {
		for _, candidate := range candidates {
			if candidate.ID == id {
				return candidate, nil
			}
		}

		return device.Info{}, fmt.Errorf("no Garmin USB device with ID %q", id)
	}

	if len(candidates) != 1 {
		return device.Info{}, fmt.Errorf("found %d Garmin USB devices; connect one watch or use -device ID", len(candidates))
	}

	return candidates[0], nil
}
