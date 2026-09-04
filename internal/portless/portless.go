package portless

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func Available() bool {
	_, err := exec.LookPath("portless")
	return err == nil
}

func Register(name string, port int) (string, error) {
	if !Available() {
		return "", errors.New("portless is not installed")
	}
	if _, err := run("alias", name, strconv.Itoa(port), "--force"); err != nil {
		return "", fmt.Errorf("register Portless route: %w", err)
	}
	url, err := run("get", name, "--no-worktree")
	if err != nil || url == "" {
		return "https://" + name + ".localhost", nil
	}
	return strings.Fields(url)[0], nil
}

func Unregister(name string) error {
	if !Available() {
		return nil
	}
	if _, err := run("alias", "--remove", name); err != nil {
		return fmt.Errorf("remove Portless route: %w", err)
	}
	return nil
}

func run(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := exec.CommandContext(ctx, "portless", args...).CombinedOutput()
	output := strings.TrimSpace(string(result))
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", errors.New("command timed out")
	}
	if err != nil {
		if output == "" {
			return "", err
		}
		return "", errors.New(output)
	}
	return output, nil
}
