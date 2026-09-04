package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/leonkozlowski/portle/internal/config"
	"github.com/leonkozlowski/portle/internal/kube"
	"github.com/leonkozlowski/portle/internal/model"
	"github.com/leonkozlowski/portle/internal/portless"
	"github.com/leonkozlowski/portle/internal/ports"
	"github.com/leonkozlowski/portle/internal/state"
)

type AddPodOptions struct {
	Pod        string
	Name       string
	Namespace  string
	RemotePort int
	LocalPort  int
	Protocol   model.Protocol
	Context    string
	Portless   bool
}

func AddPod(options AddPodOptions) (model.Target, error) {
	options.Pod = strings.TrimSpace(options.Pod)
	options.Name = strings.TrimSpace(options.Name)
	options.Namespace = strings.TrimSpace(options.Namespace)
	options.Context = strings.TrimSpace(options.Context)
	if options.Pod == "" {
		return model.Target{}, errors.New("pod name must not be empty")
	}
	if options.Name == "" {
		options.Name = options.Pod
	}
	if options.Namespace == "" {
		options.Namespace = "default"
	}
	ports, err := kube.PodPorts(options.Pod, options.Namespace, options.Context)
	if err != nil {
		return model.Target{}, err
	}
	if options.RemotePort == 0 {
		switch len(ports) {
		case 0:
			return model.Target{}, fmt.Errorf("pod %q declares no container ports; pass --port", options.Pod)
		case 1:
			options.RemotePort = ports[0]
		default:
			return model.Target{}, fmt.Errorf("pod %q declares multiple container ports %v; pass --port", options.Pod, ports)
		}
	}
	target := model.Target{
		Name:       options.Name,
		Namespace:  options.Namespace,
		Resource:   "pod/" + options.Pod,
		RemotePort: options.RemotePort,
		LocalPort:  options.LocalPort,
		Protocol:   options.Protocol,
		Context:    options.Context,
		Portless:   options.Portless,
	}
	target.ApplyDefaults()
	err = AddTarget(target)
	return target, err
}

func AddTarget(target model.Target) error {
	target.ApplyDefaults()
	if err := target.Validate(); err != nil {
		return err
	}
	return state.WithLock(func() error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if _, exists := cfg.Target(target.Name); exists {
			return fmt.Errorf("target %q already exists", target.Name)
		}
		cfg.Targets = append(cfg.Targets, target)
		return config.Save(cfg)
	})
}

func Target(name string) (model.Target, bool, error) {
	cfg, err := config.Load()
	if err != nil {
		return model.Target{}, false, err
	}
	target, found := cfg.Target(strings.TrimSpace(name))
	return target, found, nil
}

func UpdateTarget(original, updated model.Target) error {
	updated.ApplyDefaults()
	if err := updated.Validate(); err != nil {
		return err
	}
	return state.WithLock(func() error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		index := -1
		for currentIndex, current := range cfg.Targets {
			if current.Name == original.Name {
				if current != original {
					return fmt.Errorf("target %q changed while it was being edited; reopen it and try again", original.Name)
				}
				index = currentIndex
				break
			}
		}
		if index == -1 {
			return fmt.Errorf("unknown target %q", original.Name)
		}

		current, err := state.Load()
		if err != nil {
			return err
		}
		current, err = reconcileAndClean(current)
		if err != nil {
			return err
		}
		if _, running := current.Find(original.Name); running {
			return fmt.Errorf("target %q is up; run `portle down %s` before editing it", original.Name, original.Name)
		}
		if err := state.Save(current); err != nil {
			return err
		}

		cfg.Targets[index] = updated
		return config.Save(cfg)
	})
}

func DeleteTarget(name string) (bool, error) {
	name = strings.TrimSpace(name)
	wasRunning := false
	err := state.WithLock(func() error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		index := -1
		for currentIndex, target := range cfg.Targets {
			if target.Name == name {
				index = currentIndex
				break
			}
		}
		if index == -1 {
			return fmt.Errorf("unknown target %q", name)
		}

		current, err := state.Load()
		if err != nil {
			return err
		}
		current, err = reconcileAndClean(current)
		if err != nil {
			return err
		}
		if forward, running := current.Find(name); running {
			wasRunning = true
			if err := stopAndUnregister(forward); err != nil {
				return err
			}
			current.Remove(name)
		}
		if err := state.Save(current); err != nil {
			return err
		}

		cfg.Targets = append(cfg.Targets[:index], cfg.Targets[index+1:]...)
		return config.Save(cfg)
	})
	return wasRunning, err
}

func Up(name string) (model.Forward, bool, error) {
	cfg, err := config.Load()
	if err != nil {
		return model.Forward{}, false, err
	}
	target, found := cfg.Target(name)
	if !found {
		return model.Forward{}, false, fmt.Errorf("unknown target %q", name)
	}
	if target.Portless && !portless.Available() {
		return model.Forward{}, false, errors.New("this target requires Portless, but `portless` is not on PATH")
	}

	var result model.Forward
	reused := false
	err = state.WithLock(func() error {
		current, err := state.Load()
		if err != nil {
			return err
		}
		current, err = reconcileAndClean(current)
		if err != nil {
			return err
		}
		if existing, exists := current.Find(name); exists {
			if existing.Matches(target) {
				result = existing
				reused = true
				return state.Save(current)
			}
			if err := stopAndUnregister(existing); err != nil {
				return err
			}
			current.Remove(name)
		}

		localPort, err := ports.Pick(target.LocalPort)
		if err != nil {
			return err
		}
		dir, err := config.EnsureDir()
		if err != nil {
			return err
		}
		forward, err := kube.Start(target, localPort, dir)
		if err != nil {
			return err
		}
		if target.Portless {
			forward.FriendlyURL, err = portless.Register(target.Name, localPort)
			if err != nil {
				_ = kube.Stop(forward)
				return err
			}
		}

		current.Upsert(forward)
		if err := state.Save(current); err != nil {
			_ = stopAndUnregister(forward)
			return err
		}
		result = forward
		return nil
	})
	return result, reused, err
}

func Down(name string) (bool, error) {
	found := false
	var cleanupErr error
	err := state.WithLock(func() error {
		current, err := state.Load()
		if err != nil {
			return err
		}
		forward, exists := current.Find(name)
		if !exists {
			return nil
		}
		found = true
		cleanupErr = stopAndUnregister(forward)
		if cleanupErr != nil {
			return nil
		}
		current.Remove(name)
		return state.Save(current)
	})
	if err != nil {
		return found, err
	}
	return found, cleanupErr
}

func List() ([]model.TargetStatus, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	statuses := make([]model.TargetStatus, 0, len(cfg.Targets))
	err = state.WithLock(func() error {
		current, err := state.Load()
		if err != nil {
			return err
		}
		current, err = reconcileAndClean(current)
		if err != nil {
			return err
		}
		if err := state.Save(current); err != nil {
			return err
		}
		for _, target := range cfg.Targets {
			status := model.TargetStatus{Target: target}
			if forward, exists := current.Find(target.Name); exists {
				copy := forward
				status.Forward = &copy
			}
			statuses = append(statuses, status)
		}
		return nil
	})
	return statuses, err
}

func Running(name string) (model.Forward, bool, error) {
	var result model.Forward
	found := false
	err := state.WithLock(func() error {
		current, err := state.Load()
		if err != nil {
			return err
		}
		current, err = reconcileAndClean(current)
		if err != nil {
			return err
		}
		if err := state.Save(current); err != nil {
			return err
		}
		result, found = current.Find(name)
		return nil
	})
	return result, found, err
}

func ConnectionString(forward model.Forward) string {
	if forward.FriendlyURL != "" {
		return forward.FriendlyURL
	}
	if forward.Protocol == model.ProtocolHTTP {
		return fmt.Sprintf("http://127.0.0.1:%d", forward.LocalPort)
	}
	return fmt.Sprintf("127.0.0.1:%d", forward.LocalPort)
}

type Check struct {
	Name   string
	OK     bool
	Detail string
}

func Doctor() []Check {
	checks := make([]Check, 0, 6)
	_, kubectlErr := exec.LookPath("kubectl")
	checks = append(checks, Check{Name: "kubectl", OK: kubectlErr == nil, Detail: errorDetail(kubectlErr)})
	if kubectlErr == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result, err := exec.CommandContext(ctx, "kubectl", "cluster-info").CombinedOutput()
		cancel()
		detail := ""
		if err != nil {
			detail = string(result)
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				detail = "timed out after 5s"
			}
		}
		checks = append(checks, Check{Name: "cluster", OK: err == nil, Detail: detail})
	}

	cfg, configErr := config.Load()
	configPath, _ := config.Path()
	checks = append(checks, Check{Name: "config", OK: configErr == nil, Detail: chooseDetail(configErr, configPath)})
	if configErr == nil {
		checks = append(checks, Check{Name: "targets", OK: true, Detail: fmt.Sprintf("%d configured", len(cfg.Targets))})
		needsPortless := false
		for _, target := range cfg.Targets {
			needsPortless = needsPortless || target.Portless
		}
		if needsPortless {
			checks = append(checks, Check{Name: "portless", OK: portless.Available(), Detail: "required by configured targets"})
		}
	}

	stateErr := state.WithLock(func() error {
		current, err := state.Load()
		if err != nil {
			return err
		}
		alive := state.Reconcile(current)
		stale := len(current.Forwards) - len(alive.Forwards)
		checks = append(checks, Check{Name: "state", OK: true, Detail: fmt.Sprintf("%d alive, %d stale", len(alive.Forwards), stale)})
		return nil
	})
	if stateErr != nil {
		checks = append(checks, Check{Name: "state", OK: false, Detail: stateErr.Error()})
	}
	return checks
}

func stopAndUnregister(forward model.Forward) error {
	stopErr := kube.Stop(forward)
	if errors.Is(stopErr, kube.ErrProcessMismatch) {
		stopErr = nil
	}
	var routeErr error
	if forward.FriendlyURL != "" {
		routeErr = portless.Unregister(forward.Name)
	}
	return errors.Join(stopErr, routeErr)
}

func reconcileAndClean(current model.State) (model.State, error) {
	alive := make([]model.Forward, 0, len(current.Forwards))
	for _, forward := range current.Forwards {
		if kube.IsForwardAlive(forward) {
			alive = append(alive, forward)
			continue
		}
		if forward.FriendlyURL != "" {
			if err := portless.Unregister(forward.Name); err != nil {
				return current, err
			}
		}
	}
	current.Forwards = alive
	return current, nil
}

func errorDetail(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func chooseDetail(err error, success string) string {
	if err != nil {
		return err.Error()
	}
	return success
}
