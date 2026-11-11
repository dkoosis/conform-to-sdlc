package values_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dkoosis/conform-to-sdlc/internal/values"
)

func TestLoad_Valid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		want values.Values
	}{
		{
			name: "loto: tool profile, no-git-ops declared exception",
			file: "testdata/loto.json",
			want: values.Values{
				Profile: values.ProfileTool,
				Exceptions: []values.Exception{
					{Rule: "no-git-ops", Reason: "loto never performs git operations by design; declared exception, never fix"},
				},
			},
		},
		{
			name: "incubator: tool profile, incubator-extras exception",
			file: "testdata/incubator.json",
			want: values.Values{
				Profile: values.ProfileTool,
				Exceptions: []values.Exception{
					{Rule: "incubator-extras", Reason: "this repo is the incubator; carries experimental scaffolding (extra scripts, exploratory configs) other fleet repos don't declare"},
				},
			},
		},
		{
			name: "lint-set drop: per-linter reasons, no top-level reason",
			file: "testdata/lint_set_drop.json",
			want: values.Values{
				Profile: values.ProfileTool,
				Exceptions: []values.Exception{
					{Rule: "lint-set", Drop: map[string]string{"dupl": "generated tables repeat by design"}},
				},
			},
		},
		{
			name: "atomicfile: lib profile, zero exceptions",
			file: "testdata/atomicfile.json",
			want: values.Values{
				Profile:    values.ProfileLib,
				Exceptions: []values.Exception{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := values.Load(tt.file)
			if err != nil {
				t.Fatalf("Load(%q) returned error: %v", tt.file, err)
			}

			if got.Profile != tt.want.Profile {
				t.Errorf("Profile = %q, want %q", got.Profile, tt.want.Profile)
			}

			if len(got.Exceptions) != len(tt.want.Exceptions) {
				t.Fatalf("Exceptions = %+v, want %+v", got.Exceptions, tt.want.Exceptions)
			}

			for i, exc := range got.Exceptions {
				if !reflect.DeepEqual(exc, tt.want.Exceptions[i]) {
					t.Errorf("Exceptions[%d] = %+v, want %+v", i, exc, tt.want.Exceptions[i])
				}
			}
		})
	}
}

func TestLoad_Invalid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		file        string
		wantErrIs   error
		wantContain []string // substrings the error message must contain
	}{
		{
			name:        "unknown top-level key",
			file:        "testdata/unknown_key.json",
			wantContain: []string{"testdata/unknown_key.json", "extra"},
		},
		{
			name:        "unknown key inside an exception",
			file:        "testdata/unknown_key_nested.json",
			wantContain: []string{"testdata/unknown_key_nested.json", "severity"},
		},
		{
			name:        "missing reason",
			file:        "testdata/missing_reason.json",
			wantErrIs:   values.ErrEmptyReason,
			wantContain: []string{"testdata/missing_reason.json", "exception 0", "no-git-ops"},
		},
		{
			name:        "drop on a rule other than lint-set",
			file:        "testdata/drop_other_rule.json",
			wantErrIs:   values.ErrDropRule,
			wantContain: []string{"testdata/drop_other_rule.json", "exception 0", "no-git-ops"},
		},
		{
			name:        "empty rule",
			file:        "testdata/empty_rule.json",
			wantErrIs:   values.ErrEmptyRule,
			wantContain: []string{"testdata/empty_rule.json", "exception 0"},
		},
		{
			name:        "bad profile value",
			file:        "testdata/bad_profile.json",
			wantErrIs:   values.ErrInvalidProfile,
			wantContain: []string{"testdata/bad_profile.json", "app"},
		},
		{
			name:        "missing profile",
			file:        "testdata/missing_profile.json",
			wantErrIs:   values.ErrInvalidProfile,
			wantContain: []string{"testdata/missing_profile.json"},
		},
		{
			name:        "duplicate rule",
			file:        "testdata/duplicate_rule.json",
			wantErrIs:   values.ErrDuplicateRule,
			wantContain: []string{"testdata/duplicate_rule.json", "exception 1", "no-git-ops"},
		},
		{
			name:        "trailing data after the object",
			file:        "testdata/trailing_data.json",
			wantErrIs:   values.ErrTrailingData,
			wantContain: []string{"testdata/trailing_data.json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := values.Load(tt.file)
			if err == nil {
				t.Fatalf("Load(%q) returned nil error, want one", tt.file)
			}

			if tt.wantErrIs != nil && !errors.Is(err, tt.wantErrIs) {
				t.Errorf("Load(%q) error = %v, want errors.Is(_, %v)", tt.file, err, tt.wantErrIs)
			}

			for _, sub := range tt.wantContain {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("Load(%q) error = %q, want it to contain %q", tt.file, err.Error(), sub)
				}
			}
		})
	}
}

// TestLoad_Dogfood pins conform-to-sdlc's own docs/conform.json against this
// package's schema — the loader must be able to parse the file it exists to
// validate.
func TestLoad_Dogfood(t *testing.T) {
	t.Parallel()

	got, err := values.Load("../../docs/conform.json")
	if err != nil {
		t.Fatalf("Load(../../docs/conform.json) returned error: %v", err)
	}

	if got.Profile != values.ProfileTool {
		t.Errorf("Profile = %q, want %q", got.Profile, values.ProfileTool)
	}

	if len(got.Exceptions) != 0 {
		t.Errorf("Exceptions = %+v, want none", got.Exceptions)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := values.Load("testdata/does_not_exist.json")
	if err == nil {
		t.Fatal("Load of a missing file returned nil error, want one")
	}

	if !strings.Contains(err.Error(), "testdata/does_not_exist.json") {
		t.Errorf("error = %q, want it to name the path", err.Error())
	}
}
