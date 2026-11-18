package connect

import (
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/testdump"
)

func TestConnectModelContracts(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value any
		hash  string
	}{
		{name: "status zero", value: CloudStatus{}, hash: "d66a1545db838d2c521ca981d6eccc603d42d06852b30a57d85f65fd6b38ad61"},
		{name: "setting zero", value: CloudSetting{}, hash: "59b98e9c070881f7659ad2b55421da068e92f90a357ec582e8a3653107d80a0a"},
		{name: "apply nil", value: CloudApplyResult{}, hash: "f16767efcb5601cec0b6c9233ceacbc45e3faf935307a0b56bbc7962685f452e"},
		{name: "message zero", value: DeviceMessage{}, hash: "720d22db835ad2af143a80ce6d2be90ed2750666197338e6845ad37546e933a1"},
		{name: "upload nil", value: UploadResult{}, hash: "465474fceb5dd503e28cf290243a18467b7ef1f733f62847cbb235a5e07c2af7"},
		{name: "upload empty", value: UploadResult{Files: []UploadedFile{}}, hash: "2ad1b01bff41aa05f4094c63b5db0912f4e6a8a0a16b6fe6e246feeecc178930"},
		{name: "sync nil", value: SyncResult{}, hash: "b4a50f3abde8488b5ea1e2c2abb89737c24ce16d795d14b2804604100dfd2798"},
		{name: "sync empty", value: SyncResult{Pulled: []PulledFile{}}, hash: "3ccf9890b4b2f7e1c506dadac2e90543eefe0636f8ea89b296d28e42e3584ec5"},
	} {
		t.Run(test.name, func(t *testing.T) { t.Parallel(); testdump.CheckJSON(t, test.value, test.hash) })
	}
}
