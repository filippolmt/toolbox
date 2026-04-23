package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// TestCompletionWritesToCmdOut verifies `completion <shell>` writes the
// generated script to cmd.OutOrStdout instead of os.Stdout, so tests can
// capture it and `toolbox completion bash > file` redirection still works.
func TestCompletionWritesToCmdOut(t *testing.T) {
	cases := []struct {
		shell    string
		wantSubs []string
	}{
		// Every cobra-generated completion script mentions the program name.
		{"bash", []string{"toolbox"}},
		{"zsh", []string{"toolbox"}},
		{"fish", []string{"toolbox"}},
	}

	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			var buf bytes.Buffer
			completionCmd.SetOut(&buf)
			t.Cleanup(func() { completionCmd.SetOut(nil) })

			if err := completionCmd.RunE(completionCmd, []string{tc.shell}); err != nil {
				t.Fatalf("RunE(%s) error: %v", tc.shell, err)
			}
			got := buf.String()
			if got == "" {
				t.Fatalf("%s completion: empty output", tc.shell)
			}
			for _, want := range tc.wantSubs {
				if !strings.Contains(got, want) {
					t.Errorf("%s completion missing %q in output (%d bytes)", tc.shell, want, len(got))
				}
			}
		})
	}
}
