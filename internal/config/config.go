package config

import (
	"flag"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the single source of server settings
// Priority: command-line flags > YAML file > defaults.
type Config struct {
	Name     string        `yaml:"name"`
	Datadir  string        `yaml:"datadir"`
	Database string        `yaml:"database"`
	Port     int           `yaml:"port"`
	OpsPort  int           `yaml:"ops_port"`
	Verbose  bool          `yaml:"verbose"`
	Session  SessionConfig `yaml:"session"` // auth/token settings
}

// SessionConfig mirrors session.*
type SessionConfig struct {
	EncryptionKey        string `yaml:"encryption_key"`
	RefreshEncryptionKey string `yaml:"refresh_encryption_key"`
	TokenExpirySec       int    `yaml:"token_expiry_sec"`
	RefreshExpirySec     int    `yaml:"refresh_token_expiry_sec"`
}

// Defaults returns the out-of-the-box configuration.
func Defaults() *Config {
	return &Config{
		Name:     "helix",
		Datadir:  "data",
		Database: "postgres:localdb@localhost:5432/helix",
		Port:     7350,
		OpsPort:  7351,
		Session: SessionConfig{
			// Nakama ships with "defaultencryptionkey" and warns to change it.
			EncryptionKey:        "defaultencryptionkey",
			RefreshEncryptionKey: "defaultrefreshencryptionkey",
			TokenExpirySec:       3600,
			RefreshExpirySec:     86400,
		},
	}
}

// Load performs three-pass parsing
//
//	Pass 1: parse flags into scratch variables (only --config matters here)
//	Pass 2: load YAML on top of Defaults
//	Pass 3: apply ONLY explicitly-set flags (fs.Visit) over the file
func Load(args []string) (*Config, error) {
	cfg := Defaults()

	// --- Pass 1: all flags into temporary variables ---
	var (
		configPath string
		fName      string
		fDatadir   string
		fDatabase  string
		fPort      int
		fOpsPort   int
		fVerbose   bool
	)
	fs := flag.NewFlagSet("helix", flag.ContinueOnError)
	fs.StringVar(&configPath, "config", "", "path to config.yml")
	fs.StringVar(&fName, "name", "", "node name")
	fs.StringVar(&fDatadir, "datadir", "", "working directory")
	fs.StringVar(&fDatabase, "database.address", "", "Postgres DSN")
	fs.IntVar(&fPort, "port", 0, "client port")
	fs.IntVar(&fOpsPort, "ops.port", 0, "admin port")
	fs.BoolVar(&fVerbose, "verbose", false, "verbose logs")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	// --- Pass 2: YAML on top of Defaults ---
	if configPath != "" {
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("config file: %w", err)
		}
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("config parse: %w", err)
		}
	}

	// --- Pass 3: explicitly-set flags override the file ---
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "name":
			cfg.Name = fName
		case "datadir":
			cfg.Datadir = fDatadir
		case "database.address":
			cfg.Database = fDatabase
		case "port":
			cfg.Port = fPort
		case "ops.port":
			cfg.OpsPort = fOpsPort
		case "verbose":
			cfg.Verbose = fVerbose
		}
	})

	return cfg, nil
}
