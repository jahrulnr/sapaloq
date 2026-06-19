package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func ConfigPath(envPath string, cfg Config) string {
	if envPath != "" {
		return ExpandPath(envPath)
	}
	return filepath.Join(RuntimeDirs(cfg).DataDir, "config.json")
}

func LoadRaw(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := DefaultConfig()
			raw, mErr := structToMap(cfg)
			return raw, mErr
		}
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func SaveRaw(path string, raw map[string]any, updatedBy string) error {
	raw["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	if updatedBy != "" {
		raw["updatedBy"] = updatedBy
	}
	b, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func ApplyPatch(raw map[string]any, patch map[string]any, allowedPrefixes []string) error {
	for key, value := range patch {
		path := "/" + key
		if !pathAllowed(path, allowedPrefixes) {
			return fmt.Errorf("patch path %q is not allowed", path)
		}
		if existing, ok := raw[key]; ok {
			if existingMap, ok := existing.(map[string]any); ok {
				if patchMap, ok := value.(map[string]any); ok {
					mergeMap(existingMap, patchMap)
					raw[key] = existingMap
					continue
				}
			}
		}
		raw[key] = value
	}
	return nil
}

func pathAllowed(path string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, prefix := range allowed {
		if prefix == "" {
			continue
		}
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

func mergeMap(dst, src map[string]any) {
	for k, v := range src {
		if existing, ok := dst[k]; ok {
			if dstMap, ok := existing.(map[string]any); ok {
				if srcMap, ok := v.(map[string]any); ok {
					mergeMap(dstMap, srcMap)
					continue
				}
			}
		}
		dst[k] = v
	}
}

func structToMap(cfg Config) (map[string]any, error) {
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func ReloadFromRaw(path string) (Config, error) {
	cfg := DefaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	cfg.Runtime.DataDir = ExpandPath(defaultIfEmpty(cfg.Runtime.DataDir, defaultDataDir))
	cfg.Events.Bus.SocketPath = ExpandPath(defaultIfEmpty(cfg.Events.Bus.SocketPath, "~/.config/sapaloq/run/sapaloq.sock"))
	cfg.Commands = cfg.Commands.WithDefaults()
	return cfg, nil
}
