package secret

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"sync"

	"quotaball/internal/atomicfile"
)

type Store struct {
	path string
	mu   sync.Mutex
}

type record struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

var (
	protectSecret   = protect
	unprotectSecret = unprotect
)

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
	plain, err := unprotectSecret(raw)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (s *Store) Set(key, value string) error {
	return s.Update(map[string]string{key: value})
}

func (s *Store) Update(values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := s.read()
	if err != nil {
		return err
	}
	for key, value := range values {
		if value == "" {
			delete(data, key)
			continue
		}
		protected, err := protectSecret([]byte(value))
		if err != nil {
			return err
		}
		data[key] = record{Kind: "dpapi", Value: base64.StdEncoding.EncodeToString(protected)}
	}
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
		return nil, err
	}
	return out, nil
}

func (s *Store) write(data map[string]record) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, raw, 0o600)
}
