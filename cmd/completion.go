package cmd

import (
	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish]",
	Short: "Generate shell completion scripts",
	Long: `Generate shell completion scripts for toolbox.

Bash:
  $ source <(toolbox completion bash)
  # Persistent (macOS):
  $ toolbox completion bash > $(brew --prefix)/etc/bash_completion.d/toolbox

Zsh:
  $ toolbox completion zsh > "${fpath[1]}/_toolbox"

Fish:
  $ toolbox completion fish > ~/.config/fish/completions/toolbox.fish`,
	ValidArgs:             []string{"bash", "zsh", "fish"},
	Args:                  usageArgs(cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs)),
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		switch args[0] {
		case "bash":
			return cmd.Root().GenBashCompletionV2(out, true)
		case "zsh":
			return cmd.Root().GenZshCompletion(out)
		case "fish":
			return cmd.Root().GenFishCompletion(out, true)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(completionCmd)
}
