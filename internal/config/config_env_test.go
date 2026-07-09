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

// TestMergePreservesEnvKeyCase guards the viper key-lowercasing fix: env: and
// shells.<name>.env hold case-sensitive variable names, so Merge must keep their
// original case (viper lowercases every unmarshalled key).
func TestMergePreservesEnvKeyCase(t *testing.T) {
	y := []byte("shell: zsh\n" +
		"env:\n  CLAUDE_CODE_WORKFLOWS: \"1\"\n  MixedCase: v\n" +
		"shells:\n  infra:\n    path: /tmp/infra\n    env:\n      PER_SHELL_VAR: s\n")
	cfg, err := Merge(y, nil, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if cfg.Env["CLAUDE_CODE_WORKFLOWS"] != "1" || cfg.Env["MixedCase"] != "v" {
		t.Errorf("top-level env keys lowercased: %v", cfg.Env)
	}
	if got := cfg.Shells["infra"].Env["PER_SHELL_VAR"]; got != "s" {
		t.Errorf("per-shell env key lowercased: %v", cfg.Shells["infra"].Env)
	}
}

// TestMergeEnvKeyCaseHonoursLayerPrecedence locks the per-key project-over-global
// overlay in the case-restoring re-parse (same precedence viper merges layers).
func TestMergeEnvKeyCaseHonoursLayerPrecedence(t *testing.T) {
	global := []byte("env:\n  GLOBAL_ONLY: g\n  SHARED: fromglobal\n")
	project := []byte("env:\n  SHARED: fromproject\n  ProjKey: p\n")
	cfg, err := Merge(global, project, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	want := map[string]string{"GLOBAL_ONLY": "g", "SHARED": "fromproject", "ProjKey": "p"}
	if len(cfg.Env) != len(want) {
		t.Fatalf("Merge env = %v, want %v", cfg.Env, want)
	}
	for k, v := range want {
		if cfg.Env[k] != v {
			t.Errorf("env[%q] = %q, want %q", k, cfg.Env[k], v)
		}
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
