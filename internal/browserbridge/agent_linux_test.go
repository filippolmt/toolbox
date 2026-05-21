//go:build linux

package browserbridge

import (
	"strings"
	"testing"
)

func TestRenderUnit_ContainsExpectedKeys(t *testing.T) {
	got, err := renderUnit("/usr/local/bin/toolbox")
	if err != nil {
		t.Fatal(err)
	}
	mustContain := []string{
		"[Unit]",
		"Description=Toolbox Browser Bridge",
		"[Service]",
		"ExecStart=/usr/local/bin/toolbox browser-bridge daemon",
		"Restart=on-failure",
		"[Install]",
		"WantedBy=default.target",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("unit missing %q\n---\n%s", s, got)
		}
	}
}
