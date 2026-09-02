package config

import "testing"

// TestMergeImageReclaim asserts `image_reclaim` round-trips through the
// layered merge as a genuine tri-state. The distinction the pointer carries is
// load-bearing: an absent key must stay distinguishable from a written
// `false`, or a project layer that says nothing would re-arm an act the global
// layer disabled in so many words.
func TestMergeImageReclaim(t *testing.T) {
	tests := []struct {
		name    string
		global  string
		project string
		want    *bool
	}{
		{name: "absent_stays_unset"},
		{name: "project_opts_out", project: "image_reclaim: false\n", want: boolPtr(false)},
		{name: "global_opts_out", global: "image_reclaim: false\n", want: boolPtr(false)},
		{name: "project_true_wins", global: "image_reclaim: false\n", project: "image_reclaim: true\n", want: boolPtr(true)},
		{name: "project_false_wins", global: "image_reclaim: true\n", project: "image_reclaim: false\n", want: boolPtr(false)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Merge([]byte(tc.global), []byte(tc.project), nil)
			if err != nil {
				t.Fatalf("Merge: %v", err)
			}
			switch {
			case tc.want == nil && cfg.ImageReclaim != nil:
				t.Errorf("ImageReclaim = %v, want unset", *cfg.ImageReclaim)
			case tc.want != nil && cfg.ImageReclaim == nil:
				t.Errorf("ImageReclaim unset, want %v", *tc.want)
			case tc.want != nil && *cfg.ImageReclaim != *tc.want:
				t.Errorf("ImageReclaim = %v, want %v", *cfg.ImageReclaim, *tc.want)
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }
