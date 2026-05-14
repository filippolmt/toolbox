package workspace

import "testing"

func TestValidateRejectsColon(t *testing.T) {
	err := Validate("/Users/alice/foo:bar/project")
	if err == nil {
		t.Fatal("paths with ':' must be rejected to avoid bind-format mis-parsing")
	}
}

func TestValidateAcceptsCommonPaths(t *testing.T) {
	cases := []string{
		"/Users/alice/project",
		"/home/bob/code-with-dashes",
		"/mnt/data/dir.with.dots",
		"/tmp/a_b_c",
		"/",
	}
	for _, p := range cases {
		if err := Validate(p); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", p, err)
		}
	}
}
