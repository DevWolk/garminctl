package clix

import (
	"errors"
	"flag"
	"strings"
)

// ParseFlags parses args and turns a bad flag into a usage error.
func ParseFlags(fs *flag.FlagSet, args []string) error {
	err := fs.Parse(args)
	if err != nil && !errors.Is(err, flag.ErrHelp) {
		return &UsageError{msg: err.Error()}
	}

	return err
}

// FlagsFirst moves "-flag" tokens ahead of positional arguments so that
// "create FILE -send" parses like "create -send FILE". A standalone "--"
// ends reordering and protects all following tokens as positional arguments.
func FlagsFirst(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			flags = append(flags, "--")

			return append(flags, pos...)
		}

		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}

		flags = append(flags, a)

		if takesValue(fs, a) && i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}

	return append(flags, pos...)
}

// takesValue identifies known non-boolean flags without an inline value.
func takesValue(fs *flag.FlagSet, a string) bool {
	name := strings.TrimLeft(a, "-")
	if strings.Contains(name, "=") {
		return false
	}

	f := fs.Lookup(name)
	if f == nil {
		return false
	}

	b, ok := f.Value.(interface{ IsBoolFlag() bool })

	return !ok || !b.IsBoolFlag()
}
