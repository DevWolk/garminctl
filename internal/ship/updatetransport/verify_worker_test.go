package updatetransport_test

import (
	"io"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/ship/updatetransport"
)

func TestVerifyWorkerRequiresCompiledKeyBeforeInput(t *testing.T) {
	t.Parallel()

	input := strings.NewReader("untrusted protocol")

	err := updatetransport.RunVerifyWorker(t.Context(), updatetransport.WorkerConfig{Directory: t.TempDir()}, input, io.Discard)
	if err == nil || input.Len() != len("untrusted protocol") {
		t.Fatalf("error=%v remaining=%d", err, input.Len())
	}
}
