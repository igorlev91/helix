package config

import (
	"flag"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config — единый источник настроек сервера
// Приоритет: флаги командной строки > YAML-файл > значения по умолчанию.
type Config struct {
	Name     string `yaml:"name"`     // имя ноды (в будущем — для кластера)
	Datadir  string `yaml:"datadir"`  // рабочая директория (данные, модули)
	Database string `yaml:"database"` // DSN Postgres: user:pass@host:5432/dbname
	Port     int    `yaml:"port"`     // клиентский порт
	OpsPort  int    `yaml:"ops_port"` // порт админки/метрик
	Verbose  bool   `yaml:"verbose"`  // подробное логирование
}

// Defaults — конфигурация "из коробки", если ничего не задано.
func Defaults() *Config {
	return &Config{
		Name:     "helix",
		Datadir:  "data",
		Database: "postgres:localdb@localhost:5432/helix",
		Port:     7350,
		OpsPort:  7351,
	}
}

// Load — двухпроходный парсинг
//
//	Проход 1: парсим только --config (остальные флаги — в черновые переменные)
//	Проход 2: грузим YAML поверх Defaults
//	Проход 3: применяем ТОЛЬКО явно заданные флаги (fs.Visit) поверх файла
func Load(args []string) (*Config, error) {
	cfg := Defaults()

	// --- Проход 1: все флаги во временные переменные ---
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
	fs.StringVar(&configPath, "config", "", "путь к config.yml")
	fs.StringVar(&fName, "name", "", "имя ноды")
	fs.StringVar(&fDatadir, "datadir", "", "рабочая директория")
	fs.StringVar(&fDatabase, "database.address", "", "DSN Postgres")
	fs.IntVar(&fPort, "port", 0, "клиентский порт")
	fs.IntVar(&fOpsPort, "ops.port", 0, "порт админки")
	fs.BoolVar(&fVerbose, "verbose", false, "подробные логи")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	// --- Проход 2: YAML поверх Defaults ---
	if configPath != "" {
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("config file: %w", err)
		}
		if err := yaml.Unmarshal(raw, cfg); err != nil {
			return nil, fmt.Errorf("config parse: %w", err)
		}
	}

	// --- Проход 3: флаги, заданные явно, перекрывают файл ---
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
