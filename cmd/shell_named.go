package cmd

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"github.com/filippolmt/toolbox/internal/workspace"
)

var shellStdinIsTerminal = term.IsTerminal

func resolveShellWorkspace(args []string, create bool, createPath string) (string, string, error) {
	if len(args) == 0 {
		ws, err := workspace.Resolve()
		return ws, "", err
	}

	name := args[0]
	path, ok := shellPathFor(name)
	if !ok {
		return bootstrapMissingNamedShell(name, create, createPath)
	}
	if path == "" {
		return "", "", fmt.Errorf("error: shell %q has empty path", name)
	}

	return ensureNamedShellPath(name, path, create)
}

func shellPathFor(name string) (string, bool) {
	if cfg == nil || cfg.Shells == nil {
		return "", false
	}
	s, ok := cfg.Shells[name]
	if !ok {
		return "", false
	}
	return strings.TrimSpace(s.Path), true
}

func bootstrapMissingNamedShell(name string, create bool, createPath string) (string, string, error) {
	path := defaultShellPath(name)
	if createPath != "" {
		path = createPath
	}

	if create {
		if err := upsertShellInUserConfig(name, path); err != nil {
			return "", "", err
		}
		return ensureNamedShellPath(name, path, true)
	}

	if !shellStdinIsTerminal(int(os.Stdin.Fd())) {
		return "", "", errors.New(missingShellHint(name))
	}

	reader := bufio.NewReader(os.Stdin)
	chosenPath, err := promptPath(reader, os.Stderr, name, path)
	if err != nil {
		return "", "", err
	}

	createDir, err := promptYesNo(reader, os.Stderr, "  create directory?", true)
	if err != nil {
		return "", "", err
	}
	addConfig, err := promptYesNo(reader, os.Stderr, "  add to ~/.toolbox.yaml?", true)
	if err != nil {
		return "", "", err
	}

	if addConfig {
		if err := upsertShellInUserConfig(name, chosenPath); err != nil {
			return "", "", err
		}
	}
	return ensureNamedShellPath(name, chosenPath, createDir)
}

func ensureNamedShellPath(name, path string, createDir bool) (string, string, error) {
	if !filepath.IsAbs(path) {
		return "", "", fmt.Errorf("error: path %s is not absolute", path)
	}
	if err := workspace.Validate(path); err != nil {
		return "", "", err
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			if createDir {
				if mkErr := os.MkdirAll(path, 0o755); mkErr != nil {
					return "", "", fmt.Errorf("create %s: %w", path, mkErr)
				}
			} else {
				return "", "", errors.New(missingPathHint(name, path))
			}
		} else {
			return "", "", fmt.Errorf("stat %s: %w", path, err)
		}
	}
	return path, name, nil
}

func defaultShellPath(name string) string {
	return filepath.Join("/tmp", name)
}

func missingShellHint(name string) string {
	path := defaultShellPath(name)
	return fmt.Sprintf(`error: shell %q not configured

Add to ~/.toolbox.yaml:

  shells:
    %s:
      path: %s

Then create the directory:

  mkdir -p %s

Or run with auto-bootstrap:

  toolbox shell %s --create`, name, name, path, path, name)
}

func missingPathHint(name, path string) string {
	return fmt.Sprintf(`error: path %s does not exist

Create it:

  mkdir -p %s

Or re-run with auto-create:

  toolbox shell %s --create`, path, path, name)
}

func promptPath(r *bufio.Reader, w io.Writer, name, defaultPath string) (string, error) {
	_, _ = fmt.Fprintf(w, "shell %q not configured.\n\n", name)
	_, _ = fmt.Fprintf(w, "  path [%s]: ", defaultPath)
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultPath, nil
	}
	return line, nil
}

func promptYesNo(r *bufio.Reader, w io.Writer, label string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	_, _ = fmt.Fprintf(w, "%s %s ", label, suffix)
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return defaultYes, nil
	}
	switch line {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return defaultYes, nil
	}
}

func upsertShellInUserConfig(name, path string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	cfgPath := filepath.Join(home, ".toolbox.yaml")

	var root yaml.Node
	b, readErr := os.ReadFile(cfgPath)
	switch {
	case readErr == nil:
		if len(bytes.TrimSpace(b)) == 0 {
			root = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
		} else if err := yaml.Unmarshal(b, &root); err != nil {
			return fmt.Errorf("parse %s: %w", cfgPath, err)
		}
	case os.IsNotExist(readErr):
		root = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	default:
		return fmt.Errorf("read %s: %w", cfgPath, readErr)
	}

	doc := ensureDocumentMap(&root)
	shells := ensureChildMap(doc, "shells")
	entry := ensureChildMap(shells, name)
	setMapValue(entry, "path", path)

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return fmt.Errorf("encode %s: %w", cfgPath, err)
	}
	_ = enc.Close()

	if err := os.WriteFile(cfgPath, out.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", cfgPath, err)
	}
	return nil
}

func ensureDocumentMap(root *yaml.Node) *yaml.Node {
	if root.Kind == 0 {
		root.Kind = yaml.DocumentNode
	}
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 || root.Content[0] == nil {
			root.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
		}
		if root.Content[0].Kind != yaml.MappingNode {
			root.Content[0] = &yaml.Node{Kind: yaml.MappingNode}
		}
		return root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		root.Kind = yaml.MappingNode
		root.Content = nil
	}
	return root
}

func ensureChildMap(parent *yaml.Node, key string) *yaml.Node {
	if parent.Kind != yaml.MappingNode {
		parent.Kind = yaml.MappingNode
		parent.Content = nil
	}
	for i := 0; i+1 < len(parent.Content); i += 2 {
		k := parent.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			v := parent.Content[i+1]
			if v.Kind != yaml.MappingNode {
				v.Kind = yaml.MappingNode
				v.Content = nil
			}
			return v
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := &yaml.Node{Kind: yaml.MappingNode}
	parent.Content = append(parent.Content, k, v)
	return v
}

func setMapValue(parent *yaml.Node, key, value string) {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		k := parent.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			parent.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
			return
		}
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
	)
}
