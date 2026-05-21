package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestBrowserBridge_RejectsExtraArgs(t *testing.T) {
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	rootCmd.SetArgs([]string{"browser-bridge", "install", "extra"})
	var buf bytes.Buffer
	rootCmd.SetErr(&buf)
	rootCmd.SetOut(&buf)
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "accepts 0 arg") &&
		!strings.Contains(err.Error(), "unknown command") {
		t.Errorf("err = %v", err)
	}
}
