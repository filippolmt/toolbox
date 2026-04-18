package mount

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/filippolmt/toolbox/internal/config"
)

// ResolveMounts espande i path con ~ e verifica l'esistenza.
// Path mancanti producono warning e vengono skippati (D-09).
// T-02-01: filepath.Clean() applicato su tutti i path dopo espansione.
func ResolveMounts(mounts []config.Mount) (resolved []string, warnings []string) {
	home, err := os.UserHomeDir()
	if err != nil {
		warnings = append(warnings, "impossibile determinare home directory: "+err.Error())
		return nil, warnings
	}

	for _, m := range mounts {
		src := expandHome(m.Source, home)
		src = filepath.Clean(src) // T-02-01: path sanitization

		if _, err := os.Stat(src); os.IsNotExist(err) {
			warnings = append(warnings, "path non trovato, mount skippato: "+m.Source)
			continue
		}

		mode := "rw"
		if m.ReadOnly {
			mode = "ro"
		}
		resolved = append(resolved, src+":"+m.Target+":"+mode)
	}

	return resolved, warnings
}

// expandHome sostituisce il prefisso ~ con la home directory.
func expandHome(path string, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:])
	}
	return path
}
