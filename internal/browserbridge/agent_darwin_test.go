//go:build darwin

package browserbridge

import (
	"strings"
	"testing"
)

func TestRenderPlist_ContainsExpectedKeys(t *testing.T) {
	got, err := renderPlist("/opt/homebrew/bin/toolbox", "/Users/u/Library/Logs/toolbox-browser.log")
	if err != nil {
		t.Fatal(err)
	}
	mustContain := []string{
		"<key>Label</key>",
		"<string>com.filippolmt.toolbox.browser</string>",
		"<string>/opt/homebrew/bin/toolbox</string>",
		"<string>browser-bridge</string>",
		"<string>daemon</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<key>KeepAlive</key>",
		"<key>StandardOutPath</key>",
		"/Users/u/Library/Logs/toolbox-browser.log",
	}
	for _, s := range mustContain {
		if !strings.Contains(got, s) {
			t.Errorf("plist missing %q\n---\n%s", s, got)
		}
	}
}
