package model

import (
	"fmt"
	"strings"
)

type Protocol string

const (
	ProtocolTCP  Protocol = "tcp"
	ProtocolHTTP Protocol = "http"
)

type Target struct {
	Name       string   `yaml:"name"`
	Namespace  string   `yaml:"namespace,omitempty"`
	Resource   string   `yaml:"resource"`
	RemotePort int      `yaml:"remote_port"`
	LocalPort  int      `yaml:"local_port,omitempty"`
	Protocol   Protocol `yaml:"protocol,omitempty"`
	Context    string   `yaml:"context,omitempty"`
	Portless   bool     `yaml:"portless,omitempty"`
}

func (t *Target) ApplyDefaults() {
	t.Name = strings.TrimSpace(t.Name)
	t.Namespace = strings.TrimSpace(t.Namespace)
	t.Resource = strings.TrimSpace(t.Resource)
	t.Context = strings.TrimSpace(t.Context)
	if t.Namespace == "" {
		t.Namespace = "default"
	}
	if t.Protocol == "" {
		t.Protocol = ProtocolTCP
	}
}

func (t Target) Validate() error {
	if t.Name == "" {
		return fmt.Errorf("name must not be empty")
	}
	if t.Resource == "" {
		return fmt.Errorf("target %q: resource must not be empty", t.Name)
	}
	if t.RemotePort < 1 || t.RemotePort > 65535 {
		return fmt.Errorf("target %q: remote_port must be between 1 and 65535", t.Name)
	}
	if t.LocalPort < 0 || t.LocalPort > 65535 {
		return fmt.Errorf("target %q: local_port must be between 1 and 65535", t.Name)
	}
	if t.Protocol != ProtocolTCP && t.Protocol != ProtocolHTTP {
		return fmt.Errorf("target %q: protocol must be tcp or http", t.Name)
	}
	if t.Portless && t.Protocol != ProtocolHTTP {
		return fmt.Errorf("target %q: portless requires protocol: http", t.Name)
	}
	return nil
}

type Config struct {
	Targets []Target `yaml:"targets"`
}

func (c *Config) NormalizeAndValidate() error {
	seen := make(map[string]struct{}, len(c.Targets))
	for i := range c.Targets {
		c.Targets[i].ApplyDefaults()
		if err := c.Targets[i].Validate(); err != nil {
			return err
		}
		if _, exists := seen[c.Targets[i].Name]; exists {
			return fmt.Errorf("duplicate target name %q", c.Targets[i].Name)
		}
		seen[c.Targets[i].Name] = struct{}{}
	}
	return nil
}

func (c Config) Target(name string) (Target, bool) {
	for _, target := range c.Targets {
		if target.Name == name {
			return target, true
		}
	}
	return Target{}, false
}

type Forward struct {
	Name            string   `json:"name"`
	PID             int      `json:"pid"`
	ProcessIdentity string   `json:"process_identity"`
	LocalPort       int      `json:"local_port"`
	RemotePort      int      `json:"remote_port"`
	Resource        string   `json:"resource"`
	Namespace       string   `json:"namespace"`
	Protocol        Protocol `json:"protocol"`
	Context         string   `json:"context,omitempty"`
	FriendlyURL     string   `json:"friendly_url,omitempty"`
}

func (f Forward) Matches(target Target) bool {
	return f.Name == target.Name &&
		f.RemotePort == target.RemotePort &&
		f.Resource == target.Resource &&
		f.Namespace == target.Namespace &&
		f.Protocol == target.Protocol &&
		f.Context == target.Context &&
		(target.LocalPort == 0 || f.LocalPort == target.LocalPort) &&
		(target.Portless == (f.FriendlyURL != ""))
}

type State struct {
	Forwards []Forward `json:"forwards"`
}

func (s State) Find(name string) (Forward, bool) {
	for _, forward := range s.Forwards {
		if forward.Name == name {
			return forward, true
		}
	}
	return Forward{}, false
}

func (s *State) Upsert(forward Forward) {
	filtered := s.Forwards[:0]
	for _, current := range s.Forwards {
		if current.Name != forward.Name {
			filtered = append(filtered, current)
		}
	}
	s.Forwards = append(filtered, forward)
}

func (s *State) Remove(name string) {
	filtered := s.Forwards[:0]
	for _, forward := range s.Forwards {
		if forward.Name != name {
			filtered = append(filtered, forward)
		}
	}
	s.Forwards = filtered
}

type TargetStatus struct {
	Target  Target
	Forward *Forward
}
