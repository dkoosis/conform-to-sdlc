package main

import "testing"

// TestVersionDefault: a binary built with no -ldflags (a plain `go build`,
// same as `go test`) leaves version at its zero-config default, never empty.
func TestVersionDefault(t *testing.T) {
	if version != "unknown" {
		t.Errorf("version = %q, want %q (no -ldflags in this build)", version, "unknown")
	}
}

// TestParseModeVersion: `repo-standard version` is handled (no further check runs)
// and reports no error.
func TestParseModeVersion(t *testing.T) {
	mode, handled, err := parseMode([]string{"version"})
	if !handled {
		t.Errorf("handled = false, want true")
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if mode != "" {
		t.Errorf("mode = %q, want empty", mode)
	}
}
