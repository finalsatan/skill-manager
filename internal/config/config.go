// Package config reads and writes skillet's user configuration file.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// ErrNotInitialized is returned by Load when no config file exists yet.
var ErrNotInitialized = errors.New("skillet is not initialized; run `skillet init` first")

// CustomAgent lets the user add an agent or override a built-in one.
type CustomAgent struct {
	ID           string   `toml:"id"`
	Name         string   `toml:"name,omitempty"`
	ProjectDir   string   `toml:"project_dir,omitempty"`
	GlobalDir    string   `toml:"global_dir,omitempty"`
	ProjectReads []string `toml:"project_reads,omitempty"`
	GlobalReads  []string `toml:"global_reads,omitempty"`
}

type Config struct {
	Store         string        `toml:"store"`
	CheckInterval string        `toml:"check_interval,omitempty"`
	LinkMode      string        `toml:"link_mode,omitempty"`
	Agents        []CustomAgent `toml:"agents,omitempty"`
}

// Default returns the configuration written by `skillet init`.
func Default() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Config{
		Store:         filepath.Join(home, ".skillet"),
		CheckInterval: "6h",
		LinkMode:      "auto",
	}, nil
}

// Dir is the directory holding config.toml. SKILLET_CONFIG_DIR overrides it.
func Dir() (string, error) {
	if d := os.Getenv("SKILLET_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "skillet"), nil
}

func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	cfg, err := Default()
	if err != nil {
		return nil, err
	}
	if _, err := toml.DecodeFile(p, cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotInitialized
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	if cfg.Store, err = ExpandHome(cfg.Store); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Save(cfg *Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ExpandHome expands a leading ~ and returns an absolute, cleaned path.
func ExpandHome(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, p[1:])
	}
	return filepath.Abs(p)
}
