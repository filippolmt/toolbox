package config

import "testing"

// TestValidateKeyAgreesWithTheValidationTail: ValidateKey exists so a surface
// holding one raw key/value pair (the `config set` flags today) can fail fast
// with the *same* verdict the load path reaches later. The oracle is therefore
// Merge — the real validation tail over a resolved Config — not a restatement of
// each validator here.
func TestValidateKeyAgreesWithTheValidationTail(t *testing.T) {
	cases := []struct {
		key, good, bad string
	}{
		{"image", "ghcr.io/acme/box:v1", "http://ghcr.io/acme/box:v1"},
		{"registry_mirror", "harbor.corp.io/ghcr-proxy", "/harbor.corp.io"},
		{"pull", "never", "sometimes"},
		{"agent", SupportedAgents[0], "notanagent"},
		{"shell", SupportedShells[0], "bash"},
		{"mounts_root", "~/toolbox-state", "relative/dir"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			if err := ValidateKey(tc.key, tc.good); err != nil {
				t.Errorf("ValidateKey(%q, %q) = %v, want nil", tc.key, tc.good, err)
			}
			if _, err := Merge(nil, []byte(tc.key+": "+tc.good+"\n"), nil); err != nil {
				t.Errorf("the tail rejects the good value too — fixture is wrong: %v", err)
			}

			keyErr := ValidateKey(tc.key, tc.bad)
			if keyErr == nil {
				t.Fatalf("ValidateKey(%q, %q) = nil, want an error", tc.key, tc.bad)
			}
			_, tailErr := Merge(nil, []byte(tc.key+": "+tc.bad+"\n"), nil)
			if tailErr == nil {
				t.Fatalf("the tail accepts %q: %q, so ValidateKey is stricter than the load path", tc.key, tc.bad)
			}
			if keyErr.Error() != tailErr.Error() {
				t.Errorf("verdicts differ:\n  ValidateKey: %v\n  tail:        %v", keyErr, tailErr)
			}
		})
	}
}

// TestValidateKeyIsSilentWhereTheTailNeedsAWholeConfig: keys whose validator
// cannot judge a lone string — bool toggles, and the structural keys the tail
// validates over the resolved Config — must return nil rather than invent a
// verdict. The write gate is what catches those.
func TestValidateKeyIsSilentWhereTheTailNeedsAWholeConfig(t *testing.T) {
	for _, key := range []string{"bridge", "proximo", "mounts", "shells", "env", "sdd", "worktree", "inherit_host_auth"} {
		if err := ValidateKey(key, "anything"); err != nil {
			t.Errorf("ValidateKey(%q, …) = %v, want nil", key, err)
		}
	}
	if err := ValidateKey("no_such_key", "x"); err != nil {
		t.Errorf("ValidateKey on an unknown key = %v, want nil", err)
	}
}
