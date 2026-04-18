package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "toolbox",
	Short: "Manage the toolbox development container",
	Long:  "CLI to start, stop, and build the toolbox container.",
}

// Execute runs the root command. Invoked from main.go.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "",
		"config file (default: .toolbox.yaml, ~/.toolbox.yaml)")
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		// 1. Built-in defaults
		setDefaults()

		// 2. Global config (~/.toolbox.yaml)
		home, _ := os.UserHomeDir()
		viper.SetConfigName(".toolbox")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(home)
		_ = viper.ReadInConfig() // ok if missing (D-05)

		// 3. Project config (.toolbox.yaml) -- merged on top of global (D-04)
		viper.AddConfigPath(".")
		_ = viper.MergeInConfig() // project wins over global
	}

	// 4. Env var overrides (TOOLBOX_IMAGE_NAME, etc.)
	viper.SetEnvPrefix("TOOLBOX")
	viper.AutomaticEnv()
}

// setDefaults sets default values per individual field.
// Do NOT use nested objects; that breaks MergeInConfig (Pitfall 2).
// Default mounts are handled in config.Load() as a fallback.
func setDefaults() {
	viper.SetDefault("image.name", "toolbox")
	viper.SetDefault("image.tag", "local")
	viper.SetDefault("build.context", ".")
	viper.SetDefault("build.dockerfile", "docker/Dockerfile")
}
