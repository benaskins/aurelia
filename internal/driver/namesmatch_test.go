package driver

import "testing"

func TestNamesMatchTruncatedComm(t *testing.T) {
	// macOS kern.proc P_comm caps process names at 16 chars (MAXCOMLEN), so a
	// longer binary name is reported truncated. The truncated name must still
	// match the full recorded command, or VerifyProcess wrongly treats a live
	// service as an orphan (observed wedging the daemon on "werkhaus-dashboard").
	actual := "werkhaus-dashboa"     // 16 chars, as reported by P_comm
	expected := "werkhaus-dashboard" // 18 chars, full binary name

	if !namesMatch(actual, expected) {
		t.Errorf("namesMatch(%q, %q) = false, want true (P_comm truncates to 16 chars)", actual, expected)
	}
}
