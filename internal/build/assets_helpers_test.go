package build

import (
	"io/fs"
	"strings"
	"testing"
)

func readAsset(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(Assets, AssetDir+"/"+name)
	if err != nil {
		t.Fatalf("read embedded %s: %v", name, err)
	}
	return string(b)
}

// finalStage returns the text of the Dockerfile's final stage — the RUN tail and
// the COPY block ADR 0002's layer ordering is about.
//
// The anchor is the LAST `FROM` in the file, which is the final stage by
// definition. It used to be the last `FROM node:`, and that broke silently the
// day the node image was named once as `node-base`: every stage then derived
// from the alias, the only literal `node:` left was the alias declaration near
// the top, and the helper handed every caller the whole file from there down.
// Nothing went red — the tests that read this all ask "does the final stage
// contain X", and a superset answers yes.
func finalStage(t *testing.T) string {
	t.Helper()
	body := readAsset(t, "Dockerfile")
	from := strings.LastIndex(body, "\nFROM ")
	if from < 0 {
		t.Fatal("Dockerfile: cannot locate the final stage — no FROM found")
	}
	return body[from+1:]
}
