package bridge

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// shimDir is the on-disk root of the container-side bridge shims, relative to
// this package.
const shimDir = "../build/assets/bin/"

// readShim returns the on-disk body of one container-side shim.
func readShim(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(shimDir + name)
	if err != nil {
		t.Fatalf("read shim %s: %v", name, err)
	}
	return string(raw)
}

// TestBridgeContract_ShimMatchesGo binds the daemon↔shim wire contract: every
// container-side path, route and JSON field the shell shims hardcode must
// equal the Go constant the daemon serves. They live in two languages linked
// only by comments ("Must match BRIDGE_SOCK in bridge-lib.sh"), and a JSON
// struct tag cannot reference a constant, so nothing but a test holds the two
// ends together — a rename on either side otherwise breaks the bridge
// silently. Mirrors the init.d + Tool Catalog bijection tests: drift is a red
// test, not a field report.
func TestBridgeContract_ShimMatchesGo(t *testing.T) {
	// post is the shim-side spelling of a route: the transport call in
	// bridge-lib.sh takes the endpoint as its first argument.
	post := func(route string) string { return "bridge_post " + route }
	// field is the shim-side spelling of a request key — the one form shared
	// by the printf templates and the python3 encoders.
	field := func(name string) string { return `"` + name + `":` }
	// get is the shim-side spelling of a response key read back via python3.
	get := func(name string) string { return `resp.get("` + name + `"` }

	for _, tc := range []struct {
		shim     string
		literals []struct{ name, literal string }
	}{
		{"bridge-lib.sh", []struct{ name, literal string }{
			{"ContainerDir", ContainerDir},
			{"LegacyContainerDir", LegacyContainerDir},
			{"ContainerSocket", ContainerSocket},
			{"tokenFile", tokenFile},
			{"portFile", portFile},
		}},
		{"xdg-open", []struct{ name, literal string }{
			{"RouteOpen", post(RouteOpen)},
			{"FieldURL", field(FieldURL)},
		}},
		{"code", []struct{ name, literal string }{
			{"RouteEdit", post(RouteEdit)},
			{"FieldEditor", field(FieldEditor)},
			{"FieldPath", field(FieldPath)},
		}},
		{"proximo", []struct{ name, literal string }{
			{"RouteProximo", post(RouteProximo)},
			{"FieldCommand", field(FieldCommand)},
			{"FieldOutput", get(FieldOutput)},
			{"FieldExit", get(FieldExit)},
		}},
		{"git-credential-toolbox", []struct{ name, literal string }{
			{"RouteCredential", post(RouteCredential)},
			{"FieldOp", field(FieldOp)},
			{"FieldInput", field(FieldInput)},
			{"FieldOutput", get(FieldOutput)},
		}},
	} {
		t.Run(tc.shim, func(t *testing.T) {
			shim := readShim(t, tc.shim)
			for _, c := range tc.literals {
				if !strings.Contains(shim, c.literal) {
					t.Errorf("%s: shim %s does not contain %q — Go/shell bridge contract drifted", c.name, tc.shim, c.literal)
				}
			}
		})
	}
}

// TestBridgeContract_ProximoAllowlistMatchesShim pins the second half of the
// proximo gate. The shim rejects anything outside its own `case` arm before
// the daemon is ever reached, so a command added to ProximoAllowlist stays
// unreachable until the shim learns it — and one dropped from the map leaves
// a shim still advertising it. Both directions are drift, so this is a set
// equality, not a containment.
func TestBridgeContract_ProximoAllowlistMatchesShim(t *testing.T) {
	shim := readShim(t, "proximo")

	// The dispatch arm is the single line assigning COMMAND:
	//   up | down | status) COMMAND="$1" ;;
	var arm string
	for line := range strings.SplitSeq(shim, "\n") {
		if strings.Contains(line, `COMMAND="$1"`) {
			arm = line
			break
		}
	}
	if arm == "" {
		t.Fatalf(`shim proximo has no COMMAND="$1" dispatch arm — the allowlist can no longer be read from it`)
	}
	got := map[string]struct{}{}
	for alt := range strings.SplitSeq(strings.Split(arm, ")")[0], "|") {
		got[strings.TrimSpace(alt)] = struct{}{}
	}

	for cmd := range ProximoAllowlist {
		if _, ok := got[cmd]; !ok {
			t.Errorf("ProximoAllowlist has %q but shim proximo rejects it — the command is unreachable from the container", cmd)
		}
	}
	for cmd := range got {
		if _, ok := ProximoAllowlist[cmd]; !ok {
			t.Errorf("shim proximo forwards %q but ProximoAllowlist rejects it — the daemon answers 400", cmd)
		}
	}
}

// TestBridgeContract_EditorAllowlistHasShim pins the editor half: bin/code
// infers the editor from its own basename, so an allowlisted editor only
// exists in the container if the image installs a shim under that exact name
// (a COPY for `code`, a symlink for every synonym). Without one the allowlist
// entry is dead weight the daemon would happily launch on the host.
func TestBridgeContract_EditorAllowlistHasShim(t *testing.T) {
	raw, err := os.ReadFile("../build/assets/Dockerfile")
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	dockerfile := string(raw)
	for editor := range EditorAllowlist {
		if !strings.Contains(dockerfile, "/usr/local/bin/"+editor) {
			t.Errorf("EditorAllowlist has %q but the Dockerfile installs no /usr/local/bin/%s shim — the editor is unreachable from the container", editor, editor)
		}
	}
}

// TestBridgeContract_JSONTagsMatchConstants closes the one gap the compiler
// cannot: a struct tag is an uninterpreted string literal, so renaming a Field*
// constant leaves the tag — the thing actually on the wire — untouched and the
// build green. Each body type is listed with its tags in declaration order.
func TestBridgeContract_JSONTagsMatchConstants(t *testing.T) {
	for _, tc := range []struct {
		typ  reflect.Type
		tags []string
	}{
		{reflect.TypeFor[openRequest](), []string{FieldURL}},
		{reflect.TypeFor[editRequest](), []string{FieldEditor, FieldPath}},
		{reflect.TypeFor[proximoRequest](), []string{FieldCommand}},
		{reflect.TypeFor[proximoResponse](), []string{FieldExit, FieldOutput}},
		{reflect.TypeFor[credentialRequest](), []string{FieldOp, FieldInput}},
		{reflect.TypeFor[credentialResponse](), []string{FieldExit, FieldOutput}},
	} {
		t.Run(tc.typ.Name(), func(t *testing.T) {
			if got := tc.typ.NumField(); got != len(tc.tags) {
				t.Fatalf("%d fields, want %d — a body field was added or removed without updating the wire contract", got, len(tc.tags))
			}
			for i, want := range tc.tags {
				if got := tc.typ.Field(i).Tag.Get("json"); got != want {
					t.Errorf("field %s: json tag = %q, want %q", tc.typ.Field(i).Name, got, want)
				}
			}
		})
	}
}
