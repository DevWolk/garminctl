package updatetransport

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestHelperBlockedByApplicationControlIsNamedToTheUser(t *testing.T) {
	t.Parallel()

	blocked := &os.PathError{Op: "fork/exec", Path: `C:\cache\stage-1\garminctl.exe`, Err: errBlockedByPolicy}

	err := explainHelperStart(blocked)
	if err == nil || !strings.Contains(err.Error(), "Smart App Control") || !strings.Contains(err.Error(), "nothing was checked or installed") {
		t.Fatalf("explained error = %v", err)
	}

	if !errors.Is(err, errBlockedByPolicy) {
		t.Fatal("the system cause was dropped")
	}
}

func TestOtherHelperStartErrorsAreLeftAsTheyAre(t *testing.T) {
	t.Parallel()

	missing := &os.PathError{Op: "fork/exec", Path: `C:\cache\stage-1\garminctl.exe`, Err: os.ErrNotExist}

	if err := explainHelperStart(missing); err == nil || strings.Contains(err.Error(), "Application Control") || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("explained error = %v", err)
	}

	if explainHelperStart(nil) != nil {
		t.Fatal("no error became an error")
	}
}
