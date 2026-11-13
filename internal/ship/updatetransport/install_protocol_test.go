package updatetransport

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestInstallFramesRejectAmbiguousOrUnboundedInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, data string }{
		{"unknown field", "{\"protocol\":1,\"extra\":true}\n"},
		{"second value", "{} {}\n"},
		{"missing newline", "{}"},
		{"oversized", strings.Repeat(" ", maxStageRequest) + "{}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var decision installDecision

			reader := bufio.NewReaderSize(strings.NewReader(tc.data), maxStageRequest+1)
			if err := readInstallFrame(reader, &decision); err == nil {
				t.Fatal("ambiguous installer decision accepted")
			}
		})
	}
}

type shortInstallWriter struct{}

func (shortInstallWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestInstallFrameWriteChecksLimitAndShortWrite(t *testing.T) {
	t.Parallel()

	if err := writeInstallFrame(shortInstallWriter{}, installDecision{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write=%v", err)
	}

	var output bytes.Buffer

	if err := writeInstallFrame(&output, strings.Repeat("a", maxStageRequest)); err == nil || output.Len() != 0 {
		t.Fatal("oversized installer frame written")
	}
}
