package updatetransport

import (
	"strings"
	"testing"
)

func TestStageOutputRejectsOversizedFrameBeforeNewline(t *testing.T) {
	t.Parallel()

	canceled := false
	output := stageOutput{cancel: func() { canceled = true }}

	_, err := output.Write([]byte(strings.Repeat("x", maxStageFrame+1)))
	if err == nil || !canceled || len(output.partial) > maxStageFrame {
		t.Fatalf("error=%v canceled=%t buffered=%d", err, canceled, len(output.partial))
	}
}
