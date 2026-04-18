package config

import (
	"github.com/spf13/viper"
)

// Config is the top-level toolbox configuration.
type Config struct {
	Image  ImageConfig `mapstructure:"image"`
	Mounts []Mount     `mapstructure:"mounts"`
	Build  BuildConfig `mapstructure:"build"`
}

// ImageConfig configures the Docker image coordinates.
type ImageConfig struct {
	Name string `mapstructure:"name"`
	Tag  string `mapstructure:"tag"`
}

// Mount represents a host -> container volume bind.
type Mount struct {
	Source   string `mapstructure:"source"`
	Target   string `mapstructure:"target"`
	ReadOnly bool   `mapstructure:"readonly"`
}

// BuildConfig configures the Docker image build.
type BuildConfig struct {
	Context    string `mapstructure:"context"`
	Dockerfile string `mapstructure:"dockerfile"`
}

// ImageRef returns the fully qualified image reference (name:tag).
func (c *Config) ImageRef() string {
	return c.Image.Name + ":" + c.Image.Tag
}

// DefaultMounts returns the default mount set (D-07).
// ~/.secrets is intentionally NOT included (D-08).
func DefaultMounts() []Mount {
	return []Mount{
		{Source: "~/.claude", Target: "/home/toolbox/.claude", ReadOnly: false},
		{Source: "~/.gitconfig", Target: "/home/toolbox/.gitconfig", ReadOnly: true},
		{Source: "~/.gitconfig-dbm", Target: "/home/toolbox/.gitconfig-dbm", ReadOnly: true},
		{Source: "~/.ssh", Target: "/home/toolbox/.ssh", ReadOnly: true},
		{Source: "/var/run/docker.sock", Target: "/var/run/docker.sock", ReadOnly: false},
	}
}

// Load reads the configuration from Viper and applies defaults.
func Load() (*Config, error) {
	cfg := &Config{}
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, err
	}

	// Fall back to default mounts if none configured (D-07).
	if len(cfg.Mounts) == 0 {
		cfg.Mounts = DefaultMounts()
	}

	return cfg, nil
}
