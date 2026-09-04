package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/leonkozlowski/portle/internal/model"
	"gopkg.in/yaml.v3"
)

const starter = "targets: []\n"

func Dir() (string, error) {
	if override := os.Getenv("PORTLE_CONFIG_DIR"); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	return filepath.Join(home, ".config", "portle"), nil
}

func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yaml"), nil
}

func StatePath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.json"), nil
}

func EnsureDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create config directory: %w", err)
	}
	return dir, nil
}

func Load() (model.Config, error) {
	path, err := Path()
	if err != nil {
		return model.Config{}, err
	}
	return LoadPath(path)
}

func LoadPath(path string) (model.Config, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return model.Config{}, fmt.Errorf("config not found at %s; run `portle init`", path)
		}
		return model.Config{}, fmt.Errorf("open config: %w", err)
	}
	defer func() { _ = file.Close() }()

	var cfg model.Config
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return model.Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.NormalizeAndValidate(); err != nil {
		return model.Config{}, fmt.Errorf("validate config: %w", err)
	}
	return cfg, nil
}

func Save(cfg model.Config) error {
	if err := cfg.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	dir, err := EnsureDir()
	if err != nil {
		return err
	}
	var content bytes.Buffer
	encoder := yaml.NewEncoder(&content)
	encoder.SetIndent(2)
	if err := encoder.Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("close config encoder: %w", err)
	}

	temporary, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure temporary config: %w", err)
	}
	if _, err := temporary.Write(content.Bytes()); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	return os.Rename(temporaryPath, filepath.Join(dir, "config.yaml"))
}

func Init() (string, error) {
	dir, err := EnsureDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, "config.yaml")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("config already exists at %s", path)
		}
		return "", fmt.Errorf("create config: %w", err)
	}
	if _, err := file.WriteString(starter); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("write config: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close config: %w", err)
	}
	return path, nil
}
