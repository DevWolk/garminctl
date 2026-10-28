package clix

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"reflect"
	"testing"
)

func TestScalarRepresentationContract(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		input string
		want  any
	}{
		{input: "true", want: true},
		{input: "false", want: false},
		{input: "TRUE", want: "TRUE"},
		{input: "2", want: float64(2)},
		{input: "1.5", want: 1.5},
		{input: "01", want: "01"},
		{input: "1.0", want: "1.0"},
		{input: " 2", want: " 2"},
		{input: `{"x":[1,true]}`, want: map[string]any{"x": []any{float64(1), true}}},
		{input: "[1,null]", want: []any{float64(1), nil}},
		{input: "null", want: "null"},
		{input: "[invalid", want: "[invalid"},
		{input: "", want: ""},
	} {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			if got := ParseScalar(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseScalar(%q)=%#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestExitCodeKeepsHelpUsageAndCancellationPriority(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		err  error
		want int
	}{
		{name: "success"},
		{name: "wrapped help", err: fmt.Errorf("help: %w", flag.ErrHelp)},
		{name: "usage", err: UsageErrorf("bad argument %d", 7), want: 2},
		{name: "cancelled", err: context.Canceled, want: 130},
		{name: "deadline", err: context.DeadlineExceeded, want: 1},
		{name: "usage before cancellation", err: errors.Join(context.Canceled, UsageErrorf("bad flag")), want: 2},
		{name: "help before usage", err: errors.Join(flag.ErrHelp, UsageErrorf("bad flag"))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ExitCode(tt.err); got != tt.want {
				t.Fatalf("ExitCode(%v)=%d, want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestIndentedJSONContract(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := PrintJSON(&out, map[string]any{"html": "<", "empty": []string{}}); err != nil {
		t.Fatal(err)
	}

	const want = "{\n  \"empty\": [],\n  \"html\": \"\\u003c\"\n}\n"
	if out.String() != want {
		t.Fatalf("JSON=%q, want %q", out.String(), want)
	}
}

type failedOutput struct {
	first  error
	second error
	calls  int
}

func (w *failedOutput) Write([]byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		return 0, w.first
	}

	return 0, w.second
}

func TestPrinterRetainsFirstErrorFromEveryOutputMethod(t *testing.T) {
	t.Parallel()

	for name, write := range map[string]func(*Printer){
		"Write":   func(p *Printer) { _, _ = p.Write([]byte("output")) },
		"Printf":  func(p *Printer) { p.Printf("%s", "output") },
		"Println": func(p *Printer) { p.Println("output") },
		"Print":   func(p *Printer) { p.Print("output") },
		"JSON":    func(p *Printer) { _ = PrintJSON(p, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			first, second := errors.New("first failure"), errors.New("later failure")
			writer := &failedOutput{first: first, second: second}
			printer := NewPrinter(writer)
			write(printer)

			if !errors.Is(printer.Err(), first) {
				t.Fatalf("first output error=%v", printer.Err())
			}

			printer.Print("again")

			if !errors.Is(printer.Err(), first) || writer.calls != 2 {
				t.Fatalf("first error lost: error=%v calls=%d", printer.Err(), writer.calls)
			}
		})
	}
}
