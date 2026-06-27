package chat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var fileLocks sync.Map // path -> *sync.Mutex

func lockPath(path string) func() {
	v, _ := fileLocks.LoadOrStore(path, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func ensureDir(dir string) error {
	if dir == "" {
		return errors.New("dir is required")
	}
	return os.MkdirAll(dir, 0o700)
}

func readJSONFile(path string, dst any) error {
	mu := lockPath(path)
	defer mu()
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, dst)
}

func writeJSONFileAtomic(path string, v any) error {
	mu := lockPath(path)
	defer mu()
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func appendJSONL(path string, v any) error {
	mu := lockPath(path)
	defer mu()
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func readJSONL[T any](path string, fn func(T) error) error {
	mu := lockPath(path)
	defer mu()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(b) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	for dec.More() {
		var row T
		if err := dec.Decode(&row); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		if fn != nil {
			if err := fn(row); err != nil {
				return err
			}
		}
	}
	return nil
}
