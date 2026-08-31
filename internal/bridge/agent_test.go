package bridge

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestCheckNotRoot(t *testing.T) {
	tests := []struct {
		name       string
		euid       int
		goos       string
		sudoUser   string
		wantErr    bool
		wantSubstr string
	}{
		{"non-root-passes", 501, "darwin", "", false, ""},
		{"non-root-passes-even-under-sudo-env", 501, "linux", "stale", false, ""},
		{"sudo-macos", 0, "darwin", "filippo", true, "re-run without sudo, as filippo"},
		{"sudo-macos-names-the-domain", 0, "darwin", "filippo", true, "no GUI domain"},
		{"sudo-linux", 0, "linux", "filippo", true, "no user bus"},
		{"root-login-macos", 0, "darwin", "", true, "not as root"},
		{"root-login-other", 0, "windows", "", true, "per-user service"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkNotRoot(tc.euid, tc.goos, tc.sudoUser)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr {
				return
			}
			if !errors.Is(err, ErrRootService) {
				t.Errorf("err %v does not wrap ErrRootService", err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("err %q does not contain %q", err, tc.wantSubstr)
			}
		})
	}
}

// TestEnsureUserContext pins the wiring to the live process: the guard must
// fire exactly when this process is root and stay quiet otherwise. One of the
// two branches runs per environment (the Go container runs as root, CI runners
// do not), and either branch fails if the wire-up is reverted to a constant.
func TestEnsureUserContext(t *testing.T) {
	err := EnsureUserContext()
	if os.Geteuid() == 0 {
		if !errors.Is(err, ErrRootService) {
			t.Fatalf("running as root: err = %v, want ErrRootService", err)
		}
		return
	}
	if err != nil {
		t.Fatalf("running as uid %d: err = %v, want nil", os.Geteuid(), err)
	}
}
