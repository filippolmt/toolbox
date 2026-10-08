package imagefreshness

import (
	"context"
	"testing"

	"github.com/filippolmt/toolbox/internal/dockertest"
	"github.com/filippolmt/toolbox/internal/imageplan"
	"github.com/filippolmt/toolbox/internal/sessionplan"
)

func TestPrepareStartTranslatesSettlementIntoLifecycleDirective(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kind    StartKind
		outcome imageplan.Outcome
		want    Directive
	}{
		{name: "create accepts without replacement", kind: StartCreate, outcome: imageplan.OutcomeAccepted, want: Proceed},
		{name: "stopped acceptance replaces", kind: StartStopped, outcome: imageplan.OutcomeAccepted, want: Replace},
		{name: "decline postpones", kind: StartCreate, outcome: imageplan.OutcomeDeclined, want: Postpone},
		{name: "interrupt stops", kind: StartCreate, outcome: imageplan.OutcomeInterrupted, want: Interrupt},
		{name: "current proceeds", kind: StartCreate, outcome: imageplan.OutcomeCurrent, want: Proceed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := syncImage
			syncImage = func(context.Context, imageSource, sessionplan.Image, string, imageplan.Reason) imageplan.Outcome {
				return tc.outcome
			}
			t.Cleanup(func() { syncImage = old })

			session := New(&dockertest.Fake{}, Input{})
			if got := session.PrepareStart(t.Context(), tc.kind); got != tc.want {
				t.Errorf("PrepareStart() = %v, want %v", got, tc.want)
			}
		})
	}
}
