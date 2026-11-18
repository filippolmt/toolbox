package herdrcore

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// fakeHC records every hc argv and answers with out, or err.
func fakeHC(out string, err error) (CLI, *[][]string) {
	var calls [][]string
	return CLI{Run: func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		return []byte(out), err
	}}, &calls
}

func TestCLIListsMachinesFromHCsJSON(t *testing.T) {
	c, calls := fakeHC(`{"machines":[`+
		`{"name":"toolbox","target":"docker exec -i toolbox-herdr sh -c","via":true,"version":"0.1.21","session":null},`+
		`{"name":"vps","target":"can@10.0.0.7","version":"0.1.21"}]}`, nil)
	got, err := c.Machines()
	if err != nil {
		t.Fatalf("Machines: %v", err)
	}
	want := []Machine{
		{Name: "toolbox", Target: "docker exec -i toolbox-herdr sh -c", Via: true},
		{Name: "vps", Target: "can@10.0.0.7"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Machines = %+v, want %+v", got, want)
	}
	if want := []string{"machine", "ls", "--json"}; !slices.Equal((*calls)[0], want) {
		t.Errorf("argv = %q, want %q", (*calls)[0], want)
	}
}

func TestCLIRefusesUnreadableMachineList(t *testing.T) {
	c, _ := fakeHC("not json", nil)
	if _, err := c.Machines(); err == nil || !strings.Contains(err.Error(), "machine ls") {
		t.Errorf("Machines = %v, want an unreadable-output error naming the command", err)
	}
}

func TestCLIMachineArgv(t *testing.T) {
	for _, tc := range []struct {
		label string
		call  func(CLI) error
		want  []string
	}{
		{"add", func(c CLI) error { return c.Add("toolbox-work", "docker exec -i toolbox-herdr-work sh -c") },
			[]string{"machine", "add", "--name", "toolbox-work", "--install", "--via", "docker exec -i toolbox-herdr-work sh -c", "--json"}},
		{"update", func(c CLI) error { return c.Update("toolbox") }, []string{"machine", "update", "--json", "toolbox"}},
		{"remove", func(c CLI) error { return c.Remove("toolbox") }, []string{"machine", "rm", "--json", "toolbox"}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			c, calls := fakeHC("{}", nil)
			if err := tc.call(c); err != nil {
				t.Fatalf("%s: %v", tc.label, err)
			}
			if len(*calls) != 1 || !slices.Equal((*calls)[0], tc.want) {
				t.Errorf("argv = %q, want one call %q", *calls, tc.want)
			}
		})
	}
}

func TestCLIReportsAMissingHCDistinctly(t *testing.T) {
	c, _ := fakeHC("", fmt.Errorf("exec: %q: %w", "hc", exec.ErrNotFound))
	if _, err := c.Machines(); !errors.Is(err, ErrNoHC) {
		t.Errorf("Machines = %v, want ErrNoHC", err)
	}
	if err := c.Add("toolbox", "docker exec -i toolbox-herdr sh -c"); !errors.Is(err, ErrNoHC) {
		t.Errorf("Add = %v, want ErrNoHC", err)
	}
}

func TestCLICarriesHCsReason(t *testing.T) {
	c, _ := fakeHC("", &exec.ExitError{Stderr: []byte("error: toolbox has no hc\n")})
	err := c.Add("toolbox", "docker exec -i toolbox-herdr sh -c")
	if err == nil || errors.Is(err, ErrNoHC) || !strings.Contains(err.Error(), "toolbox has no hc") || !strings.Contains(err.Error(), "machine add") {
		t.Errorf("Add = %v, want hc's own reason and the subcommand", err)
	}
}

func TestViaIsBuiltFromTheContainerName(t *testing.T) {
	got, err := Via("toolbox-herdr-work")
	if err != nil || got != "docker exec -i toolbox-herdr-work sh -c" {
		t.Errorf("Via = %q, %v", got, err)
	}
	// hc runs --via as an sh line: a name that is not a plain container name never reaches it.
	for _, bad := range []string{"", "toolbox-herdr; rm -rf ~", "toolbox herdr", "-it", "$(id)"} {
		if _, err := Via(bad); err == nil {
			t.Errorf("Via(%q) accepted", bad)
		}
	}
}

func TestMachineNameFollowsTheProfile(t *testing.T) {
	for container, want := range map[string]string{"toolbox-herdr": "toolbox", "toolbox-herdr-work": "toolbox-work"} {
		if got := MachineName(container); got != want {
			t.Errorf("MachineName(%q) = %q, want %q", container, got, want)
		}
	}
}
