package updatetransport

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"testing"
)

type installInputBuffer struct{ bytes.Buffer }

func (*installInputBuffer) Close() error { return nil }

func TestPendingInstallCommitIsConsumedEvenWhenAcknowledgementIsLost(t *testing.T) {
	t.Parallel()

	for _, acknowledge := range []bool{false, true} {
		name := installLostAcknowledgement
		if acknowledge {
			name = "accepted"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			request := installRequest{Protocol: installProtocol, Operation: "owned operation", BuildVersion: "fixture", KeyID: "key"}

			var reply bytes.Buffer
			if acknowledge {
				if err := writeInstallFrame(&reply, installEvent{Protocol: request.Protocol, Operation: request.Operation, BuildVersion: request.BuildVersion, KeyID: request.KeyID, Kind: "accepted"}); err != nil {
					t.Fatal(err)
				}
			}

			input := &installInputBuffer{}
			pending := &PendingInstall{input: input, output: io.NopCloser(&reply), reader: bufio.NewReader(&reply), request: request}

			err := pending.Commit(t.Context())
			if (err == nil) != acknowledge {
				t.Fatalf("commit=%v", err)
			}

			first := input.String()
			if !pending.attempted || first == "" {
				t.Fatal("commit attempt not retained")
			}

			if err := pending.Commit(context.Background()); err == nil || input.String() != first {
				t.Fatal("commit sent more than once")
			}
		})
	}
}
