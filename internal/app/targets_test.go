package app

import (
	"strings"
	"testing"

	"github.com/leonkozlowski/portle/internal/config"
	"github.com/leonkozlowski/portle/internal/model"
)

func TestUpdateTarget(t *testing.T) {
	original := configureTarget(t)
	updated := original
	updated.RemotePort = 8080

	if err := UpdateTarget(original, updated); err != nil {
		t.Fatalf("update target: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	got, found := cfg.Target(original.Name)
	if !found {
		t.Fatalf("updated target %q not found", original.Name)
	}
	if got.RemotePort != 8080 {
		t.Fatalf("remote port = %d, want 8080", got.RemotePort)
	}
}

func TestAddTarget(t *testing.T) {
	t.Setenv("PORTLE_CONFIG_DIR", t.TempDir())
	if err := config.Save(model.Config{}); err != nil {
		t.Fatalf("save empty config: %v", err)
	}
	target := model.Target{
		Name:       "db",
		Resource:   "svc/db",
		RemotePort: 5432,
	}
	if err := AddTarget(target); err != nil {
		t.Fatalf("add target: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	got, found := cfg.Target("db")
	if !found {
		t.Fatal("added target not found")
	}
	if got.Namespace != "default" || got.Protocol != model.ProtocolTCP {
		t.Fatalf("target defaults not applied: %#v", got)
	}
	if err := AddTarget(target); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate add error = %v", err)
	}
}

func TestUpdateTargetDetectsConcurrentChange(t *testing.T) {
	original := configureTarget(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Targets[0].RemotePort = 8081
	if err := config.Save(cfg); err != nil {
		t.Fatalf("save concurrent change: %v", err)
	}

	updated := original
	updated.RemotePort = 8080
	err = UpdateTarget(original, updated)
	if err == nil || !strings.Contains(err.Error(), "changed while it was being edited") {
		t.Fatalf("update error = %v, want concurrent-change error", err)
	}
}

func TestDeleteTarget(t *testing.T) {
	original := configureTarget(t)

	wasRunning, err := DeleteTarget(original.Name)
	if err != nil {
		t.Fatalf("delete target: %v", err)
	}
	if wasRunning {
		t.Fatal("inactive target reported as running")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Targets) != 0 {
		t.Fatalf("targets = %#v, want none", cfg.Targets)
	}
}

func configureTarget(t *testing.T) model.Target {
	t.Helper()
	t.Setenv("PORTLE_CONFIG_DIR", t.TempDir())
	target := model.Target{
		Name:       "web",
		Namespace:  "default",
		Resource:   "svc/web",
		RemotePort: 80,
		Protocol:   model.ProtocolHTTP,
	}
	if err := config.Save(model.Config{Targets: []model.Target{target}}); err != nil {
		t.Fatalf("save config: %v", err)
	}
	return target
}
