package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/leonkozlowski/portle/internal/config"
	"github.com/leonkozlowski/portle/internal/kube"
	"github.com/leonkozlowski/portle/internal/model"
)

func WithLock(fn func() error) error {
	dir, err := config.EnsureDir()
	if err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "state.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open state lock: %w", err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock state: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	return fn()
}

func Load() (model.State, error) {
	path, err := config.StatePath()
	if err != nil {
		return model.State{}, err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return model.State{Forwards: []model.Forward{}}, nil
	}
	if err != nil {
		return model.State{}, fmt.Errorf("read state: %w", err)
	}
	var current model.State
	if err := json.Unmarshal(content, &current); err != nil {
		return model.State{}, fmt.Errorf("parse state: %w", err)
	}
	return current, nil
}

func Save(current model.State) error {
	dir, err := config.EnsureDir()
	if err != nil {
		return err
	}
	content, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	content = append(content, '\n')
	temporary, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return fmt.Errorf("create temporary state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure temporary state: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close state: %w", err)
	}
	return os.Rename(temporaryPath, filepath.Join(dir, "state.json"))
}

func Reconcile(current model.State) model.State {
	alive := make([]model.Forward, 0, len(current.Forwards))
	for _, forward := range current.Forwards {
		if kube.IsForwardAlive(forward) {
			alive = append(alive, forward)
		}
	}
	current.Forwards = alive
	return current
}
