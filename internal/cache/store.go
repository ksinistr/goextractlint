package cache

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"goextractlint/internal/domain"
)

const Version = 1

type (
	Store struct{ dir string }
	entry struct {
		Version  int             `json:"version"`
		Decision domain.Decision `json:"decision"`
	}
)

func New(dir string) *Store { return &Store{dir: dir} }

func (s *Store) path(key string) (string, error) {
	decoded, err := hex.DecodeString(key)
	if err != nil || len(decoded) != 32 {
		return "", fmt.Errorf("invalid cache key")
	}

	return filepath.Join(s.dir, key+".json"), nil
}

func (s *Store) Get(key string) (domain.Decision, bool, error) {
	path, err := s.path(key)
	if err != nil {
		return domain.Decision{}, false, fmt.Errorf("read cache: %w", err)
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return domain.Decision{}, false, nil
	}

	if err != nil {
		return domain.Decision{}, false, fmt.Errorf("read cache: %w", err)
	}

	var stored entry
	if json.Unmarshal(data, &stored) != nil || stored.Version != Version || stored.Decision.Validate() != nil {
		return domain.Decision{}, false, nil
	}

	return stored.Decision, true, nil
}

func (s *Store) Put(key string, decision domain.Decision) error {
	if err := decision.Validate(); err != nil {
		return fmt.Errorf("validate decision: %w", err)
	}

	path, err := s.path(key)
	if err != nil {
		return fmt.Errorf("cache path: %w", err)
	}

	if err = os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create cache directory: %w", err)
	}

	data, err := json.Marshal(entry{Version: Version, Decision: decision})
	if err != nil {
		return fmt.Errorf("encode cache: %w", err)
	}

	return writeAtomically(path, data)
}

func writeAtomically(path string, data []byte) (resultErr error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".decision-*")
	if err != nil {
		return fmt.Errorf("create temporary cache: %w", err)
	}
	defer func() {
		if err := os.Remove(file.Name()); err != nil && !errors.Is(err, os.ErrNotExist) {
			resultErr = errors.Join(resultErr, fmt.Errorf("remove temporary cache: %w", err))
		}
	}()

	if _, err := file.Write(data); err != nil {
		closeErr := file.Close()
		return fmt.Errorf("write cache: %w", errors.Join(err, closeErr))
	}

	if err := file.Sync(); err != nil {
		closeErr := file.Close()
		return fmt.Errorf("sync cache: %w", errors.Join(err, closeErr))
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("close cache: %w", err)
	}

	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("rename cache: %w", err)
	}

	return nil
}
