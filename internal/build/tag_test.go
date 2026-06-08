package build

import "testing"

// TestResolveImage covers the image-selection precedence: full override wins,
// then registry-mirror host swap, then the canonical default.
func TestResolveImage(t *testing.T) {
	tests := []struct {
		name           string
		image          string
		registryMirror string
		want           string
	}{
		{
			name: "default canonical when nothing set",
			want: DefaultRegistryImage,
		},
		{
			name:  "full image override used verbatim",
			image: "harbor.corp.io/team/toolbox:pinned",
			want:  "harbor.corp.io/team/toolbox:pinned",
		},
		{
			name:           "registry mirror swaps host, preserves path+tag",
			registryMirror: "harbor.corp.io/ghcr-proxy",
			want:           "harbor.corp.io/ghcr-proxy/filippolmt/toolbox:latest",
		},
		{
			name:           "trailing slash on mirror is trimmed",
			registryMirror: "harbor.corp.io/ghcr-proxy/",
			want:           "harbor.corp.io/ghcr-proxy/filippolmt/toolbox:latest",
		},
		{
			name:           "mirror with explicit port",
			registryMirror: "localhost:5000",
			want:           "localhost:5000/filippolmt/toolbox:latest",
		},
		{
			name:           "image override wins over mirror",
			image:          "ghcr.io/other/toolbox:dev",
			registryMirror: "harbor.corp.io/ghcr-proxy",
			want:           "ghcr.io/other/toolbox:dev",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveImage(tt.image, tt.registryMirror); got != tt.want {
				t.Errorf("ResolveImage(%q, %q) = %q, want %q", tt.image, tt.registryMirror, got, tt.want)
			}
		})
	}
}

// TestSplitRegistryHost covers the host-detection heuristic.
func TestSplitRegistryHost(t *testing.T) {
	tests := []struct {
		ref      string
		wantHost string
		wantRest string
	}{
		{"ghcr.io/filippolmt/toolbox:latest", "ghcr.io", "filippolmt/toolbox:latest"},
		{"localhost:5000/app:1", "localhost:5000", "app:1"},
		{"localhost/app:1", "localhost", "app:1"},
		{"library/alpine:3", "", "library/alpine:3"},
		{"alpine:3", "", "alpine:3"},
	}
	for _, tt := range tests {
		t.Run(tt.ref, func(t *testing.T) {
			host, rest := SplitRegistryHost(tt.ref)
			if host != tt.wantHost || rest != tt.wantRest {
				t.Errorf("SplitRegistryHost(%q) = (%q, %q), want (%q, %q)", tt.ref, host, rest, tt.wantHost, tt.wantRest)
			}
		})
	}
}
