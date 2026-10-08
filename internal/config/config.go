package config

import (
	"flag"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)


type Config struct {
	Name     string `yaml:"name"`      // имя ноды (в будущем — для кластера)
	Datadir  string `yaml:"datadir"`   // рабочая директория (данные, модули)
	Database string `yaml:"database"`  // DSN Postgres: user:pass@host:5432/dbname
	Port     int    `yaml:"port"`      // клиентский порт 
	OpsPort  int    `yaml:"ops_port"`  // порт админки/метрик
	Verbose  bool   `yaml:"verbose"`   // подробное логирование
}


func Defaults() *Config {
	return &Config{
		Name:     "helix",
		Datadir:  "data",
		Database: "postgres:localdb@localhost:5432/helix",
		Port:     7350,
		OpsPort:  7351,
	}
}


func Load(args []string) (*Config, error) {
	cfg := Defaults()

	fs := flag.NewFlagSet("helix", flag.ContinueOnError)
	configPath := fs.String("config", "", "путь к config.yml")
	fs.StringVar(&cfg.Name, "name", cfg.Name, "имя ноды")
	fs.StringVar(&cfg.Datadir, "datadir", cfg.Datadir, "рабочая директория")
	fs.StringVar(&cfg.Database, "database.address", cfg.Database, "DSN Postgres")
	fs.IntVar(&cfg.Port, "port", cfg.Port, "клиентский порт")
	fs.IntVar(&cfg.OpsPort, "ops.port", cfg.OpsPort, "порт админки")
	fs.BoolVar(&cfg.Verbose, "verbose", cfg.Verbose, "подробные логи")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if *configPath != "" {
		raw, err := os.ReadFile(*configPath)
		if err != nil {
			return nil, fmt.Errorf("config file: %w", err)
		}

		if err := yaml.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("config parse: %w", err)
		}
	}

	return cfg, nil
}
