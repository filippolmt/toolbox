package bridge

import (
	"errors"
	"strings"
	"testing"
)

func TestEvaluateCredentialHelpers(t *testing.T) {
	// notFound rejects the given helper names, accepts the rest.
	notFound := func(names ...string) func(string) (string, error) {
		miss := map[string]bool{}
		for _, n := range names {
			miss["git-credential-"+n] = true
		}
		return func(bin string) (string, error) {
			if miss[bin] {
				return "", errors.New("not found")
			}
			return "/usr/bin/" + bin, nil
		}
	}

	tests := []struct {
		name       string
		helpers    []string
		goos       string
		lookPath   func(string) (string, error)
		wantOK     bool
		wantSubstr string
	}{
		{"none-macos", nil, "darwin", notFound(), false, "osxkeychain"},
		{"none-linux", nil, "linux", notFound(), false, "libsecret"},
		{"none-other", nil, "windows", notFound(), false, "credential.helper"},
		{"osxkeychain-present", []string{"osxkeychain"}, "darwin", notFound(), true, ""},
		{"libsecret-missing", []string{"libsecret"}, "linux", notFound("libsecret"), false, "git-credential-libsecret"},
		{"builtin-store-ok", []string{"store"}, "linux", notFound("store"), true, ""},
		{"custom-shell-ignored", []string{"!/opt/foo/helper get"}, "linux", notFound(), true, ""},
		{"absolute-path-ignored", []string{"/opt/foo/git-credential-x"}, "linux", notFound(), true, ""},
		{"chain-one-missing", []string{"osxkeychain", "libsecret"}, "darwin", notFound("libsecret"), false, "libsecret"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, advice := evaluateCredentialHelpers(tc.helpers, tc.goos, tc.lookPath)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (advice=%q)", ok, tc.wantOK, advice)
			}
			if tc.wantOK {
				if advice != "" {
					t.Fatalf("advice = %q, want empty", advice)
				}
				return
			}
			if !strings.Contains(advice, tc.wantSubstr) {
				t.Fatalf("advice %q does not contain %q", advice, tc.wantSubstr)
			}
		})
	}
}
