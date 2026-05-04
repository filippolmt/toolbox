package mountplan

import "testing"

func TestWorkspaceMirrorPath(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"/Users/alice/project", "/Users/alice/project", true},
		{"/home/alice/code", "/home/alice/code", true},
		{"/mnt/data/repo", "/mnt/data/repo", true},
		{"/tmp/work/x", "/tmp/work/x", true},
		{"/", "", false},
		{WorkspaceTarget, "", false},
		{WorkspaceTarget + "/nested", "", false},
		{"/home/toolbox", "", false},
		{"/home/toolbox/app", "", false},
		{"/usr/local/src", "", false},
		{"/etc/toolbox", "", false},
		{"", "", false},
		{"relative/path", "", false},
	}
	for _, tc := range cases {
		got, ok := WorkspaceMirrorPath(tc.in)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("WorkspaceMirrorPath(%q) = (%q, %v), want (%q, %v)",
				tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}
