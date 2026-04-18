package config

import (
	"github.com/spf13/viper"
)

// Config rappresenta la configurazione completa del toolbox.
type Config struct {
	Image  ImageConfig `mapstructure:"image"`
	Mounts []Mount     `mapstructure:"mounts"`
	Build  BuildConfig `mapstructure:"build"`
}

// ImageConfig configura l'immagine Docker.
type ImageConfig struct {
	Name string `mapstructure:"name"`
	Tag  string `mapstructure:"tag"`
}

// Mount rappresenta un volume mount host -> container.
type Mount struct {
	Source   string `mapstructure:"source"`
	Target   string `mapstructure:"target"`
	ReadOnly bool   `mapstructure:"readonly"`
}

// BuildConfig configura il build dell'immagine Docker.
type BuildConfig struct {
	Context    string `mapstructure:"context"`
	Dockerfile string `mapstructure:"dockerfile"`
}

// ImageRef ritorna il riferimento completo dell'immagine (name:tag).
func (c *Config) ImageRef() string {
	return c.Image.Name + ":" + c.Image.Tag
}

// DefaultMounts ritorna i mount di default (D-07).
// ~/.secrets NON e' incluso di default (D-08).
func DefaultMounts() []Mount {
	return []Mount{
		{Source: "~/.claude", Target: "/home/toolbox/.claude", ReadOnly: false},
		{Source: "~/.gitconfig", Target: "/home/toolbox/.gitconfig", ReadOnly: true},
		{Source: "~/.gitconfig-dbm", Target: "/home/toolbox/.gitconfig-dbm", ReadOnly: true},
		{Source: "~/.ssh", Target: "/home/toolbox/.ssh", ReadOnly: true},
		{Source: "/var/run/docker.sock", Target: "/var/run/docker.sock", ReadOnly: false},
	}
}

// Load carica la configurazione da Viper e applica i defaults.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, err
	}

	// Se nessun mount configurato, usa i defaults (D-07)
	if len(cfg.Mounts) == 0 {
		cfg.Mounts = DefaultMounts()
	}

	return cfg, nil
}
