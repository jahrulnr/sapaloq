package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jahrulnr/sapaloq/internal/bridges/cursor/credentials"
)

type Config struct {
	SchemaVersion string         `json:"schemaVersion"`
	Runtime       RuntimeConfig  `json:"runtime"`
	LLMBridge     LLMBridge      `json:"llmBridge"`
	Commands      CommandsConfig `json:"commands"`
	Events        EventsConfig   `json:"events"`
}

type RuntimeConfig struct {
	DataDir    string `json:"dataDir"`
	BinaryName string `json:"binaryName"`
}

type LLMBridge struct {
	Driver         string   `json:"driver"`
	Endpoint       string   `json:"endpoint"`
	Model          string   `json:"model"`
	CredentialsEnv string   `json:"credentialsEnv"`
	DeclaredTools  []string `json:"declaredTools,omitempty"`
}

type EventsConfig struct {
	Bus BusConfig `json:"bus"`
}

type BusConfig struct {
	SocketPath string `json:"socketPath"`
}

func DefaultConfig() Config {
	return Config{
		SchemaVersion: "1.0.0",
		Runtime: RuntimeConfig{
			DataDir:    defaultDataDir,
			BinaryName: "sapaloq-core",
		},
		LLMBridge: LLMBridge{
			Driver:         "cursor-bridge",
			Endpoint:       "https://api2.cursor.sh",
			Model:          "default",
			CredentialsEnv: "SAPALOQ_CURSOR_TOKEN",
		},
		Commands: DefaultCommands(),
		Events:   EventsConfig{Bus: BusConfig{SocketPath: "~/.config/sapaloq/run/sapaloq.sock"}},
	}
}

func Load(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		path = filepath.Join("config", "config.example.json")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	cfg.Runtime.DataDir = ExpandPath(defaultIfEmpty(cfg.Runtime.DataDir, defaultDataDir))
	cfg.Events.Bus.SocketPath = ExpandPath(defaultIfEmpty(cfg.Events.Bus.SocketPath, "~/.config/sapaloq/run/sapaloq.sock"))
	cfg.LLMBridge.CredentialsEnv = defaultIfEmpty(cfg.LLMBridge.CredentialsEnv, "SAPALOQ_CURSOR_TOKEN")
	cfg.Commands = cfg.Commands.WithDefaults()
	if err := cfg.Commands.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Doctor(cfg Config) (string, error) {
	if err := cfg.Commands.Validate(); err != nil {
		return "", err
	}
	dirs := RuntimeDirs(cfg)
	if err := EnsureRuntimeDirs(dirs); err != nil {
		return "", err
	}
	creds, err := credentials.Load(credentials.Options{TokenEnv: cfg.LLMBridge.CredentialsEnv})
	if err != nil {
		return "", err
	}
	credSource := creds.Source
	probe := filepath.Join(dirs.RunDir, ".sapaloq-write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return "", fmt.Errorf("socket directory is not writable: %w", err)
	}
	_ = os.Remove(probe)
	return credSource, nil
}

func defaultIfEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
