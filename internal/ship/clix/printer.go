// Package clix provides shared CLI output, flag and scalar parsing primitives.
// It owns no watch, account or feature behavior.
package clix

import (
	"encoding/json"
	"fmt"
	"io"
)

// Printer retains the first output error for run to report.
type Printer struct {
	w   io.Writer
	err error
}

// NewPrinter wraps an output writer and retains its first write error.
func NewPrinter(w io.Writer) *Printer { return &Printer{w: w} }

func (p *Printer) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.note(err)

	return n, err
}

// Printf writes formatted output and remembers a write failure.
func (p *Printer) Printf(format string, a ...any) {
	_, err := fmt.Fprintf(p.w, format, a...)
	p.note(err)
}

// Println writes a line and remembers a write failure.
func (p *Printer) Println(a ...any) {
	_, err := fmt.Fprintln(p.w, a...)
	p.note(err)
}

// Print writes output and remembers a write failure.
func (p *Printer) Print(a ...any) {
	_, err := fmt.Fprint(p.w, a...)
	p.note(err)
}

func (p *Printer) note(err error) {
	if p.err == nil && err != nil {
		p.err = err
	}
}

// Err is the first write error, if any.
func (p *Printer) Err() error { return p.err }

// PrintJSON writes the existing indented JSON command format.
func PrintJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(value)
}
