package main

import (
	"os"

	"github.com/spf13/cobra"
)

var completionCmd = &cobra.Command{
	Use:   "completion [bash|zsh|fish|powershell]",
	Short: "Generate shell completion scripts",
	Long: `Generate shell completion scripts for lplex.

To load completions:

Bash:
  $ source <(lplex completion bash)

  # To load completions for each session, execute once:
  # Linux:
  $ lplex completion bash > /etc/bash_completion.d/lplex
  # macOS:
  $ lplex completion bash > $(brew --prefix)/etc/bash_completion.d/lplex

Zsh:
  # If shell completion is not already enabled in your environment,
  # you will need to enable it. You can execute the following once:
  $ echo "autoload -U compinit; compinit" >> ~/.zshrc

  # To load completions for each session, execute once:
  $ lplex completion zsh > "${fpath[1]}/_lplex"

  # You will need to start a new shell for this setup to take effect.

Fish:
  $ lplex completion fish | source

  # To load completions for each session, execute once:
  $ lplex completion fish > ~/.config/fish/completions/lplex.fish

PowerShell:
  PS> lplex completion powershell | Out-String | Invoke-Expression

  # To load completions for every new session, add the output to your profile:
  PS> lplex completion powershell > lplex.ps1
  # and source this file from your PowerShell profile.
`,
	DisableFlagsInUseLine: true,
	ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
	Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return cmd.Root().GenBashCompletionV2(os.Stdout, true)
		case "zsh":
			return cmd.Root().GenZshCompletion(os.Stdout)
		case "fish":
			return cmd.Root().GenFishCompletion(os.Stdout, true)
		case "powershell":
			return cmd.Root().GenPowerShellCompletionWithDesc(os.Stdout)
		}
		return nil
	},
}
