package sessionplan_test

import (
	"testing"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// TestPlanResolvesImageReclaim pins the tri-state resolution of
// `image_reclaim` onto the one bool the container edge reads. The absent case
// is the load-bearing one: Image Reclamation runs unless the developer
// disabled it in so many words, so an unwritten key must resolve to on rather
// than to the zero value of a plain bool.
func TestPlanResolvesImageReclaim(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name string
		set  *bool
		want bool
	}{
		{name: "absent_runs", want: true},
		{name: "explicit_true_runs", set: &yes, want: true},
		{name: "explicit_false_opts_out", set: &no},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			workspace := planWorkspace(t)
			cfg := &config.Config{Shell: "zsh", ImageReclaim: tc.set}
			plan, err := sessionplan.Plan(sessionplan.PlanInput{Cfg: cfg, Workspace: workspace})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if plan.ReclaimImages != tc.want {
				t.Errorf("plan.ReclaimImages = %v, want %v", plan.ReclaimImages, tc.want)
			}
		})
	}
}
