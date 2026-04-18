package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish]",
	Short: "Genera script di shell completion",
	Long: `Genera script di shell completion per toolbox.

Bash:
  $ source <(toolbox completion bash)
  # Permanente (macOS):
  $ toolbox completion bash > $(brew --prefix)/etc/bash_completion.d/toolbox

Zsh:
  $ toolbox completion zsh > "${fpath[1]}/_toolbox"

Fish:
  $ toolbox completion fish > ~/.config/fish/completions/toolbox.fish`,
	ValidArgs:             []string{"bash", "zsh", "fish"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		switch args[0] {
		case "bash":
			cmd.Root().GenBashCompletionV2(os.Stdout, true)
		case "zsh":
			cmd.Root().GenZshCompletion(os.Stdout)
		case "fish":
			cmd.Root().GenFishCompletion(os.Stdout, true)
		}
	},
}

func init() {
	rootCmd.AddCommand(completionCmd)
}
