package updatetransport_test

import (
	"strings"
	"testing"
)

func canonicalStageDACL(sddl string) string {
	// Windows may retain AI on an explicitly protected DACL. Keep P and every
	// ACE byte intact: AI is a control flag, not an inherited-ACE marker.
	if rest, ok := strings.CutPrefix(sddl, "D:PAI("); ok {
		return "D:P(" + rest
	}

	return sddl
}

func TestStageDACLComparisonPreservesAccessChecks(t *testing.T) {
	t.Parallel()

	const want = "D:P(A;OICI;FA;;;LA)"
	for _, tc := range []struct {
		name string
		got  string
		ok   bool
	}{
		{"protected", want, true},
		{"auto-inherited control", "D:PAI(A;OICI;FA;;;LA)", true},
		{"unprotected", "D:(A;OICI;FA;;;LA)", false},
		{"unprotected auto-inherited", "D:AI(A;OICI;FA;;;LA)", false},
		{"extra everyone ACE", "D:PAI(A;OICI;FA;;;LA)(A;OICI;FA;;;WD)", false},
		{"inherited ACE", "D:PAI(A;OICIID;FA;;;LA)", false},
		{"wrong owner", "D:PAI(A;OICI;FA;;;BA)", false},
		{"wrong rights", "D:PAI(A;OICI;FR;;;LA)", false},
		{"missing child inheritance", "D:PAI(A;;FA;;;LA)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := canonicalStageDACL(tc.got) == canonicalStageDACL(want); got != tc.ok {
				t.Errorf("ACL %s matches %s: got %v, want %v", tc.got, want, got, tc.ok)
			}
		})
	}
}
