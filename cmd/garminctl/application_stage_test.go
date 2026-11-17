package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DevWolk/garminctl/internal/app"
	"github.com/DevWolk/garminctl/internal/ship/clix"
)

func TestCLIPrivateStageRefusesUnconfiguredKey(t *testing.T) {
	t.Parallel()

	for _, command := range []string{app.ApplicationStageCommand, app.ApplicationVerifyCommand, app.ApplicationInstallCommand, app.ApplicationPackageCommand} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			err := runCLI([]string{command}, strings.NewReader(`{"publicKey":"untrusted"}`), &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), "compiled public key") || stdout.Len() != 0 {
				t.Fatalf("error=%v stdout=%q", err, stdout.String())
			}
		})
	}
}

func TestCLIPrivateStageRejectsArguments(t *testing.T) {
	t.Parallel()

	for _, command := range []string{app.ApplicationStageCommand, app.ApplicationVerifyCommand, app.ApplicationInstallCommand, app.ApplicationPackageCommand} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			err := runCLI([]string{command, "--public-key=untrusted"}, strings.NewReader(""), &stdout, &stderr)
			if clix.ExitCode(err) != 2 || stdout.Len() != 0 {
				t.Fatalf("error=%v stdout=%q", err, stdout.String())
			}
		})
	}
}

func TestCLIOrdinaryVersionContract(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if err := runCLI([]string{"version"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	if stdout.String() != "garminctl "+version+"\n" {
		t.Fatalf("version output=%q", stdout.String())
	}
}
