package kube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/leonkozlowski/portle/internal/model"
)

var ErrProcessMismatch = errors.New("process no longer belongs to portle")

func PodPorts(pod, namespace, contextName string) ([]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := []string{"get", "pod", pod}
	if contextName != "" {
		args = append(args, "--context", contextName)
	}
	args = append(args, "-n", namespace, "-o", "json")
	result, err := exec.CommandContext(ctx, "kubectl", args...).CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("inspect pod %q: timed out after 10s", pod)
	}
	if err != nil {
		detail := strings.TrimSpace(string(result))
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("inspect pod %q: %s", pod, detail)
	}
	var response struct {
		Spec struct {
			Containers []struct {
				Ports []struct {
					ContainerPort int `json:"containerPort"`
				} `json:"ports"`
			} `json:"containers"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, fmt.Errorf("decode pod %q: %w", pod, err)
	}
	unique := make(map[int]struct{})
	for _, container := range response.Spec.Containers {
		for _, port := range container.Ports {
			if port.ContainerPort > 0 {
				unique[port.ContainerPort] = struct{}{}
			}
		}
	}
	ports := make([]int, 0, len(unique))
	for port := range unique {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports, nil
}

func CommandArgs(target model.Target, localPort int) []string {
	args := []string{"port-forward"}
	if target.Context != "" {
		args = append(args, "--context", target.Context)
	}
	return append(args,
		"-n", target.Namespace,
		target.Resource,
		fmt.Sprintf("%d:%d", localPort, target.RemotePort),
	)
}

func Start(target model.Target, localPort int, runtimeDir string) (model.Forward, error) {
	diagnostics, err := os.CreateTemp(runtimeDir, ".kubectl-*.log")
	if err != nil {
		return model.Forward{}, fmt.Errorf("create kubectl log: %w", err)
	}
	diagnosticsPath := diagnostics.Name()
	defer func() {
		_ = diagnostics.Close()
		_ = os.Remove(diagnosticsPath)
	}()

	cmd := exec.Command("kubectl", CommandArgs(target, localPort)...)
	cmd.Stdout = diagnostics
	cmd.Stderr = diagnostics
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return model.Forward{}, fmt.Errorf("start kubectl: %w", err)
	}

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.NewTimer(10 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()

	for {
		if portReady(localPort) {
			identity := ProcessIdentity(cmd.Process.Pid)
			if identity == "" {
				terminateStarted(cmd.Process.Pid, exited)
				return model.Forward{}, errors.New("kubectl started but its process identity could not be verified")
			}
			return model.Forward{
				Name:            target.Name,
				PID:             cmd.Process.Pid,
				ProcessIdentity: identity,
				LocalPort:       localPort,
				RemotePort:      target.RemotePort,
				Resource:        target.Resource,
				Namespace:       target.Namespace,
				Protocol:        target.Protocol,
				Context:         target.Context,
			}, nil
		}

		select {
		case err := <-exited:
			if err == nil {
				err = errors.New("kubectl exited before the port became ready")
			}
			return model.Forward{}, startupError(target.Name, err, diagnostics)
		case <-deadline.C:
			terminateStarted(cmd.Process.Pid, exited)
			return model.Forward{}, startupError(target.Name, errors.New("timed out after 10s"), diagnostics)
		case <-ticker.C:
		}
	}
}

func ProcessIdentity(pid int) string {
	result, err := exec.Command(
		"ps", "-p", strconv.Itoa(pid), "-o", "lstart=", "-o", "comm=",
	).Output()
	if err != nil {
		return ""
	}
	return strings.Join(strings.Fields(string(result)), " ")
}

func IsForwardAlive(forward model.Forward) bool {
	return forward.ProcessIdentity != "" &&
		ProcessIdentity(forward.PID) == forward.ProcessIdentity
}

func Stop(forward model.Forward) error {
	if !IsForwardAlive(forward) {
		return ErrProcessMismatch
	}
	if err := signalGroup(forward.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop kubectl: %w", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !IsForwardAlive(forward) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := signalGroup(forward.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill kubectl: %w", err)
	}
	return nil
}

func portReady(port int) bool {
	connection, err := net.DialTimeout(
		"tcp4",
		fmt.Sprintf("127.0.0.1:%d", port),
		200*time.Millisecond,
	)
	if err != nil {
		return false
	}
	return connection.Close() == nil
}

func signalGroup(pid int, signal syscall.Signal) error {
	if err := syscall.Kill(-pid, signal); err == nil || !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return syscall.Kill(pid, signal)
}

func terminateStarted(pid int, exited <-chan error) {
	_ = signalGroup(pid, syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(time.Second):
		_ = signalGroup(pid, syscall.SIGKILL)
		select {
		case <-exited:
		case <-time.After(time.Second):
		}
	}
}

func startupError(name string, cause error, diagnostics *os.File) error {
	if _, err := diagnostics.Seek(0, 0); err != nil {
		return fmt.Errorf("kubectl port-forward for %q failed: %w", name, cause)
	}
	content, _ := io.ReadAll(io.LimitReader(diagnostics, 8<<10))
	detail := strings.TrimSpace(string(content))
	if detail == "" {
		return fmt.Errorf("kubectl port-forward for %q failed: %w", name, cause)
	}
	return fmt.Errorf("kubectl port-forward for %q failed: %s", name, detail)
}
