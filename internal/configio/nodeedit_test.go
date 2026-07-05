package configio

import "testing"

func TestSpliceFence(t *testing.T) {
	const (
		start = "# >>> sdd-managed/foo (toolbox)"
		end   = "# <<< sdd-managed/foo (toolbox)"
	)
	body := start + "\n.foo/\n" + end

	tests := []struct {
		name        string
		existing    string
		wantChanged bool
		want        string
	}{
		{
			name:        "empty input writes bare block",
			existing:    "",
			wantChanged: true,
			want:        body + "\n",
		},
		{
			name:        "append after newline-terminated content",
			existing:    "node_modules/\n",
			wantChanged: true,
			want:        "node_modules/\n\n" + body + "\n",
		},
		{
			name:        "append inserts blank line when content lacks trailing newline",
			existing:    "node_modules/",
			wantChanged: true,
			want:        "node_modules/\n\n" + body + "\n",
		},
		{
			name:        "replace existing block in place",
			existing:    "a/\n" + start + "\n.old/\n" + end + "\nb/\n",
			wantChanged: true,
			want:        "a/\n" + body + "\nb/\n",
		},
		{
			name:        "identical block is idempotent",
			existing:    body + "\n",
			wantChanged: false,
			want:        body + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := SpliceFence([]byte(tt.existing), start, end, body)
			if changed != tt.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}
			if string(got) != tt.want {
				t.Errorf("result mismatch:\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestRemoveFence(t *testing.T) {
	const (
		start = "# >>> sdd-managed/foo (toolbox)"
		end   = "# <<< sdd-managed/foo (toolbox)"
	)
	block := start + "\n.foo/\n" + end

	tests := []struct {
		name        string
		existing    string
		wantChanged bool
		want        string
	}{
		{
			name:        "fence absent is unchanged",
			existing:    "node_modules/\n",
			wantChanged: false,
			want:        "node_modules/\n",
		},
		{
			name:        "fence present is removed",
			existing:    block + "\n",
			wantChanged: true,
			want:        "",
		},
		{
			name:        "fence at EOF drops trailing newline",
			existing:    "a/\n" + block + "\n",
			wantChanged: true,
			want:        "a/\n",
		},
		{
			name:        "fence surrounded by other content",
			existing:    "a/\n" + block + "\nb/\n",
			wantChanged: true,
			want:        "a/\nb/\n",
		},
		{
			// SpliceFence appends with a blank-line separator; RemoveFence
			// must swallow it so write-then-remove round-trips.
			name:        "appended fence round-trips to original",
			existing:    "node_modules/\n\n" + block + "\n",
			wantChanged: true,
			want:        "node_modules/\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := RemoveFence([]byte(tt.existing), start, end)
			if changed != tt.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}
			if string(got) != tt.want {
				t.Errorf("result mismatch:\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}
