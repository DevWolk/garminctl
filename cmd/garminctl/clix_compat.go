package main

import (
	"flag"
	"io"

	"github.com/DevWolk/garminctl/internal/ship/clix"
)

// Compatibility names keep current commands stable until their CLI adapters move.
// The shared implementation lives only in clix; remove these bridges with callers.
type (
	printer    = clix.Printer
	usageError = clix.UsageError
)

func newPrinter(w io.Writer) *printer                     { return clix.NewPrinter(w) }
func usageErrorf(format string, args ...any) error        { return clix.UsageErrorf(format, args...) }
func exitCode(err error) int                              { return clix.ExitCode(err) }
func parseFlags(fs *flag.FlagSet, args []string) error    { return clix.ParseFlags(fs, args) }
func flagsFirst(fs *flag.FlagSet, args []string) []string { return clix.FlagsFirst(fs, args) }
func parseScalar(value string) any                        { return clix.ParseScalar(value) }
func (c *cli) printJSON(value any) error                  { return clix.PrintJSON(c.out, value) }
