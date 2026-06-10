//go:build darwin

package bridge

import (
	"strings"
	"testing"
)

func TestRenderPlist_ContainsExpectedKeys(t *testing.T) {
	got, err := renderPlist("/opt/homebrew/bin/toolbox", "/Users/u/Library/Logs/toolbox-bridge.log")
	if err != nil {
		t.Fatal(err)
	}
	mustContain := []string{
		"<key>Label</key>",
		"<string>com.filippolmt.toolbox.bridge</string>",
		"<string>/opt/homebrew/bin/toolbox</string>",
		"<string>bridge</string>",
		"<string>daemon</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<key>KeepAlive</key>",
		"<key>StandardOutPath</key>",
		"/Users/u/Library/Logs/toolbox-bridge.log",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("plist missing %q\n---\n%s", s, got)
		}
	}
}
