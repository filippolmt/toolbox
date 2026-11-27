// Package herdrcore is the host's herdr-core (`hc`) as `toolbox herdr` reaches
// it: the machines it knows, and adding, updating and removing the Herdr Core
// Shell's machine. → CONTEXT.md, Herdr Core Shell.
package herdrcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"regexp"
	"strings"

	"github.com/filippolmt/toolbox/internal/sessionplan"
)

// Host is the host's herdr-core: the seam `toolbox herdr` registers the Herdr
// Core Shell through. Every method returns ErrNoHC when no hc runs.
type Host interface {
	// Machines lists the machines the host's herdr-core knows.
	Machines() ([]Machine, error)
	// Add adds the machine name reached through via, letting herdr-core
	// install its own hc there when it has none or one too old.
	Add(name, via string) error
	// Update copies the host's hc to the machine and restarts it there.
	Update(name string) error
	// Remove forgets the machine; its terminals keep running there.
	Remove(name string) error
}

// Machine is one row of `hc machine ls --json`. Target is the --via command
// when Via is set, else the ssh target.
type Machine struct {
	Name   string `json:"name"`
	Target string `json:"target"`
	Via    bool   `json:"via"`
}

// ErrNoHC: the host has no herdr-core CLI on PATH.
var ErrNoHC = errors.New("no hc (herdr-core) on this host's PATH")

// CLI is Host over the hc binary.
type CLI struct {
	// Run runs hc with args and returns its stdout.
	Run func(args ...string) ([]byte, error)
}

// HostCLI is the hc on this host's PATH.
func HostCLI() CLI {
	return CLI{Run: func(args ...string) ([]byte, error) {
		// args are literals, a machine name derived from the container name,
		// and a --via line Via built from it: no user input becomes an option.
		return exec.Command("hc", args...).Output() //nolint:gosec // see the comment above
	}}
}

// jsonFlag asks hc for machine-readable output and errors.
const jsonFlag = "--json"

func (c CLI) hc(args ...string) ([]byte, error) {
	out, err := c.Run(args...)
	if err == nil {
		return out, nil
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoHC
	}
	sub := "hc " + strings.Join(args[:2], " ")
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		return nil, fmt.Errorf("%s: %s", sub, strings.TrimSpace(string(exit.Stderr)))
	}
	return nil, fmt.Errorf("%s: %w", sub, err)
}

func (c CLI) Machines() ([]Machine, error) {
	out, err := c.hc("machine", "ls", jsonFlag)
	if err != nil {
		return nil, err
	}
	var list struct {
		Machines []Machine `json:"machines"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("hc machine ls: unreadable output: %w", err)
	}
	return list.Machines, nil
}

func (c CLI) Add(name, via string) error {
	_, err := c.hc("machine", "add", "--name", name, "--install", "--via", via, jsonFlag)
	return err
}

func (c CLI) Update(name string) error {
	_, err := c.hc("machine", "update", jsonFlag, name)
	return err
}

func (c CLI) Remove(name string) error {
	_, err := c.hc("machine", "rm", jsonFlag, name)
	return err
}

// containerNameRe is Docker's container-name charset, minus a leading
// separator: nothing in it means anything to sh.
var containerNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// Via is the --via line that reaches the Herdr Core Shell called container.
// hc runs it as an sh line, so only a plain container name is accepted.
func Via(container string) (string, error) {
	if !containerNameRe.MatchString(container) {
		return "", fmt.Errorf("herdr-core: %q is not a container name to reach with docker exec", container)
	}
	return "docker exec -i " + container + " sh -c", nil
}

// MachineName is the machine a Herdr Core Shell registers as: toolbox, or
// toolbox-<profile>.
func MachineName(container string) string { return sessionplan.HerdrMachineName(container) }
