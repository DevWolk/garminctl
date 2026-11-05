package cli

import (
	"bytes"
	"testing"

	"github.com/DevWolk/garminctl/internal/testdump"
)

const connectSetSubcommand = "set"

func TestConnectCLIContracts(t *testing.T) { //nolint:paralleltest // isolates process account/cache/transport state
	for _, test := range []struct { //nolint:paralleltest // cases isolate process state
		name, mode      string
		args            []string
		json, normalize bool
		code            int
		hash            string
	}{
		{name: "account text", args: []string{"status"}, hash: "7196c5072ba90b39c614583c61afdcefd12f1da153eaac8db73e8670a9c39641"},
		{name: "account JSON", args: []string{"status"}, json: true, hash: "64a2bf5495784271c08308aff844368cd0713ca3385bf50b9fe4c2025a49a611"},
		{name: "message text", args: []string{"messages"}, hash: "5109ce395c7dfbf641986acfa1eb879320b00593ba5a9f7deb71474dbeb8f34d"},
		{name: "message JSON", args: []string{"messages"}, json: true, hash: "d09cac24b166c3a0a5495179957bc72df2f71291adb659e4a373ec33fcdcf062"},
		{name: "no queued messages", args: []string{"messages"}, mode: "empty messages", hash: "58c33d58aa479cd61fa084bbb6ad0013bfa76665813d12eebc3cf987a7a9405b"},
		{name: "settings text", args: []string{settingsContractCommand}, hash: "195b7ce657ffeae3b54a339b68b5481908d946cd575d1e2bf734215e35a73bc4"},
		{name: "settings JSON", args: []string{settingsContractCommand}, json: true, hash: "605988bcbdd33a34fd24415944aa18ddfe92b6296834d584be3646f2efe22e75"},
		{name: "settings text apply", args: []string{settingsContractCommand, connectSetSubcommand, "language=2"}, hash: "c0109f16638214290322ab8e8884d2ae205cf973294796cff32991e1165dc761"},
		{name: "settings JSON apply", args: []string{settingsContractCommand, connectSetSubcommand, "language=2"}, json: true, normalize: true, hash: "765330f9cc4769b03011af8656a2c33c987f2de1fdd7152f0da97587aa1b2d8d"},
		{name: "settings partial", args: []string{settingsContractCommand, connectSetSubcommand, "language=2"}, mode: "queue failure", hash: "135d5afeee804e6dc9dbdfee7e068346e1923f63bd623cec125c5e736e18e7c1"},
		{name: "malformed change", args: []string{settingsContractCommand, connectSetSubcommand, "bad"}, code: 1, hash: "570ec686e839800e48bc3bc66fea1be04b93ca267e41b4c045eddf1a61aebf0a"},
		{name: "sync text", args: []string{"sync"}, normalize: true, hash: "376f6eb01da8b3f05c70bf63bbfa18cc167053fc76764a0dd7ececb30c1e0218"},
		{name: "sync JSON", args: []string{"sync"}, json: true, normalize: true, hash: "3936a231b82af731faa149fbe46a308dfd6c0f437ce7e4290e5875f5f3670492"},
		{name: "sync disabled", args: []string{"sync", "-no-upload", "-no-download"}, json: true, hash: "19e03c8e8bf38e91072bac82a807ad66a9dec52654c5bac50f4cee8f7cde2d3d"},
		{name: "raw GET JSON", args: []string{"get", testdump.ConnectRawPath}, hash: "3b1999da618be700c53eb3428f3a7c6b2ab3458ca260cdb66dd24bd120e6163a"},
		{name: "unknown cloud verb", args: []string{"other"}, code: 2, hash: "b42cd5de68120836a2ac71c2ae0ca120aaebf277f8610a3791936e965f2d8b9c"},
		{name: "logout account", args: []string{"logout"}, hash: "872651e5cf8ba1c07b1cf0106d089b4b48a479674646bcfec0ff2e5d33885f06"},
	} {
		t.Run(test.name, func(t *testing.T) {
			testdump.ConnectCLIEnvironment(t, test.mode)
			root := testdump.ConnectRoot(t)

			argv := []string{rootFlag, root}
			if test.json {
				argv = append(argv, "-json")
			}

			argv = append(append(argv, "cloud"), test.args...)

			var out, diagnostics bytes.Buffer

			err := run(argv, &out, &diagnostics)
			if exitCode(err) != test.code || diagnostics.Len() != 0 {
				t.Fatalf("code=%d error=%v diagnostics=%s", exitCode(err), err, &diagnostics)
			}

			data := out.Bytes()
			if err != nil {
				data = append(data, []byte("error: "+err.Error())...)
			}

			if test.normalize {
				data = testdump.NormalizePendingPath(t, data)
			}

			testdump.CheckDigest(t, data, test.hash)
		})
	}
}
