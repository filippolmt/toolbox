package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "toolbox",
	Short: "Gestione del container di sviluppo toolbox",
	Long:  "CLI per avviare, fermare e buildare il container toolbox.",
}

// Execute esegue il root command. Chiamato da main.go.
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
		// 1. Defaults built-in
		setDefaults()

		// 2. Config globale (~/.toolbox.yaml)
		home, _ := os.UserHomeDir()
		viper.SetConfigName(".toolbox")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(home)
		_ = viper.ReadInConfig() // ok se non esiste (D-05)

		// 3. Config progetto (.toolbox.yaml) -- merge sopra globale (D-04)
		viper.AddConfigPath(".")
		_ = viper.MergeInConfig() // progetto vince su globale
	}

	// 4. Env var override (TOOLBOX_IMAGE_NAME, etc.)
	viper.SetEnvPrefix("TOOLBOX")
	viper.AutomaticEnv()
}

// setDefaults imposta i valori di default per ogni campo individuale.
// NON usare nested objects per evitare problemi con MergeInConfig (Pitfall 2).
// I mounts di default sono gestiti in config.Load() come fallback.
func setDefaults() {
	viper.SetDefault("image.name", "toolbox")
	viper.SetDefault("image.tag", "local")
	viper.SetDefault("build.context", ".")
	viper.SetDefault("build.dockerfile", "docker/Dockerfile")
}
