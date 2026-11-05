package cli

import (
	"errors"
	"flag"
	"io"
	"reflect"
	"testing"
)

const (
	testNameFlag  = "-name"
	testSendFlag  = "-send"
	testFlagFile  = "file"
	testFlagLabel = "label"
)

func TestFlagsFirstPreservesParserContracts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		pos  []string
		send bool
		text string
		num  int
	}{
		{name: "interspersed", args: []string{testFlagFile, testSendFlag, testNameFlag, testFlagLabel}, pos: []string{testFlagFile}, send: true, text: testFlagLabel},
		{name: "terminator", args: []string{testFlagFile, testSendFlag, "--", testNameFlag, testFlagLabel}, pos: []string{testFlagFile, testNameFlag, testFlagLabel}, send: true},
		{name: "initial terminator", args: []string{"--", testSendFlag, "-3", "--"}, pos: []string{testSendFlag, "-3", "--"}},
		{name: "boolean false", args: []string{testFlagFile, "-send=false"}, pos: []string{testFlagFile}},
		{name: "inline value", args: []string{testFlagFile, "--name=-send"}, pos: []string{testFlagFile}, text: testSendFlag},
		{name: "negative value", args: []string{testFlagFile, "-n", "-3"}, pos: []string{testFlagFile}, num: -3},
		{name: "dash", args: []string{"-", testSendFlag}, pos: []string{"-"}, send: true},
		{name: "terminator as value", args: []string{testFlagFile, testNameFlag, "--", testSendFlag}, pos: []string{testFlagFile}, send: true, text: "--"},
		{name: "help as positional", args: []string{"--", "-h"}, pos: []string{"-h"}},
		{name: "empty terminator", args: []string{testFlagFile, "--"}, pos: []string{testFlagFile}},
		{name: "existing trailing value flag", args: []string{testFlagFile, testNameFlag}, pos: []string{}, text: testFlagFile},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			send := fs.Bool("send", false, "send")
			text := fs.String("name", "", "name")
			num := fs.Int("n", 0, "number")

			before := append([]string(nil), tc.args...)

			if err := parseFlags(fs, flagsFirst(fs, tc.args)); err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(fs.Args(), tc.pos) || *send != tc.send || *text != tc.text || *num != tc.num {
				t.Fatalf("pos=%q send=%v name=%q n=%d", fs.Args(), *send, *text, *num)
			}

			if !reflect.DeepEqual(tc.args, before) {
				t.Fatal("rewriting flags mutated the caller's arguments")
			}
		})
	}
}

func TestFlagsFirstPreservesHelpAndUsageErrors(t *testing.T) {
	t.Parallel()

	for _, token := range []string{"-h", "--help", "-unknown", "-3", testNameFlag} {
		t.Run(token, func(t *testing.T) {
			t.Parallel()

			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			fs.SetOutput(io.Discard)
			fs.String("name", "", "name")

			args := []string{testFlagFile, token}
			if token == testNameFlag {
				args = []string{token}
			}

			err := parseFlags(fs, flagsFirst(fs, args))
			if token == "-h" || token == "--help" {
				if !errors.Is(err, flag.ErrHelp) {
					t.Fatalf("help error = %v", err)
				}

				return
			}

			if _, ok := errors.AsType[*usageError](err); !ok {
				t.Fatalf("usage error = %v", err)
			}
		})
	}
}
