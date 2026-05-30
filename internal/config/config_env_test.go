package config

import (
	"strings"
	"testing"
)

func TestValidateEnvAcceptsPlainPairs(t *testing.T) {
	err := ValidateEnv(map[string]string{
		"CLAUDE_CODE_WORKFLOWS": "1",
		"EMPTY_VALUE_OK":        "",
		"lower_case":            "x",
	})
	if err != nil {
		t.Fatalf("ValidateEnv = %v, want nil", err)
	}
}

func TestValidateEnvRejectsEmptyKey(t *testing.T) {
	err := ValidateEnv(map[string]string{"": "v"})
	if err == nil || !strings.Contains(err.Error(), "empty key") {
		t.Fatalf("ValidateEnv = %v, want empty-key error", err)
	}
}

func TestValidateEnvRejectsEqualsInKey(t *testing.T) {
	err := ValidateEnv(map[string]string{"FOO=BAR": "v"})
	if err == nil || !strings.Contains(err.Error(), "'='") {
		t.Fatalf("ValidateEnv = %v, want equals-in-key error", err)
	}
}

func TestValidateEnvRejectsReservedKeys(t *testing.T) {
	for _, k := range []string{"PWD", "TOOLBOX_HOST_WORKSPACE", "TOOLBOX_ANYTHING"} {
		if err := ValidateEnv(map[string]string{k: "v"}); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("ValidateEnv(%q) = %v, want reserved error", k, err)
		}
	}
}

// TestEffectiveEnvMergesPerShellOverTopLevel locks the per-shell-wins contract.
func TestEffectiveEnvMergesPerShellOverTopLevel(t *testing.T) {
	c := &Config{
		Env: map[string]string{"SHARED": "global", "GLOBAL_ONLY": "g"},
		Shells: map[string]NamedShell{
			"infra": {Path: "/tmp/infra", Env: map[string]string{"SHARED": "shell", "SHELL_ONLY": "s"}},
		},
	}

	got := c.EffectiveEnv("infra")
	want := map[string]string{"SHARED": "shell", "GLOBAL_ONLY": "g", "SHELL_ONLY": "s"}
	if len(got) != len(want) {
		t.Fatalf("EffectiveEnv = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("EffectiveEnv[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestEffectiveEnvUnknownShellFallsBackToTopLevel(t *testing.T) {
	c := &Config{Env: map[string]string{"FOO": "bar"}}
	if got := c.EffectiveEnv("nope"); got["FOO"] != "bar" || len(got) != 1 {
		t.Errorf("EffectiveEnv(unknown) = %v, want {FOO:bar}", got)
	}
}

func TestEffectiveEnvEmptyYieldsNil(t *testing.T) {
	if got := (&Config{}).EffectiveEnv(""); got != nil {
		t.Errorf("EffectiveEnv on empty config = %v, want nil", got)
	}
}

// TestValidationTailRejectsPerShellReservedEnv asserts the per-shell env is
// validated with the same rules and the error is namespaced to the shell.
func TestValidationTailRejectsPerShellReservedEnv(t *testing.T) {
	cfg := &Config{
		Shell:  "zsh",
		Shells: map[string]NamedShell{"infra": {Path: "/tmp/infra", Env: map[string]string{"PWD": "x"}}},
	}
	err := applyValidationTail(cfg)
	if err == nil || !strings.Contains(err.Error(), "shells.infra.env:") {
		t.Fatalf("applyValidationTail = %v, want shells.infra.env reserved error", err)
	}
}

// TestEffectiveEnvDoesNotAliasConfig guards against returning the underlying
// cfg.Env map — a mutation by the caller must not leak back into config state.
func TestEffectiveEnvDoesNotAliasConfig(t *testing.T) {
	c := &Config{Env: map[string]string{"FOO": "bar"}}
	got := c.EffectiveEnv("")
	got["FOO"] = "mutated"
	if c.Env["FOO"] != "bar" {
		t.Errorf("cfg.Env aliased: got %q after caller mutation", c.Env["FOO"])
	}
}
