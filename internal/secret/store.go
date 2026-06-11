package secret

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	path string
	mu   sync.Mutex
}

type record struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.read()
	if err != nil {
		return "", err
	}
	rec, ok := data[key]
	if !ok {
		return "", nil
	}
	if rec.Kind != "dpapi" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(rec.Value)
	if err != nil {
		return "", err
	}
	plain, err := unprotect(raw)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *Store) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.read()
	if err != nil {
		return err
	}
	if value == "" {
		delete(data, key)
		return s.write(data)
	}
	protected, err := protect([]byte(value))
	if err != nil {
		return err
	}
	data[key] = record{Kind: "dpapi", Value: base64.StdEncoding.EncodeToString(protected)}
	return s.write(data)
}

func (s *Store) read() (map[string]record, error) {
	out := map[string]record{}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, nil
	}
	return out, nil
}

func (s *Store) write(data map[string]record) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o600)
}
