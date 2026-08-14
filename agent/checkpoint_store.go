package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type FileCheckPointStore struct {
	root string
}

func NewFileCheckPointStore(root string) *FileCheckPointStore {
	return &FileCheckPointStore{root: strings.TrimSpace(root)}
}

func (s *FileCheckPointStore) Get(ctx context.Context, checkpointID string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	path, err := s.path(checkpointID)
	if err != nil {
		return nil, false, err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return content, true, nil
}

func (s *FileCheckPointStore) Set(ctx context.Context, checkpointID string, checkpoint []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.path(checkpointID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(checkpoint); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func (s *FileCheckPointStore) Delete(ctx context.Context, checkpointID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := s.path(checkpointID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *FileCheckPointStore) path(checkpointID string) (string, error) {
	if s == nil || s.root == "" {
		return "", errors.New("checkpoint store root is required")
	}
	checkpointID = strings.TrimSpace(checkpointID)
	if checkpointID == "" {
		return "", errors.New("checkpoint id is required")
	}
	hash := sha256.Sum256([]byte(checkpointID))
	return filepath.Join(s.root, hex.EncodeToString(hash[:])+".checkpoint"), nil
}
