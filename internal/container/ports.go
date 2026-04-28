package container

import (
	"fmt"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"

	"github.com/filippolmt/toolbox/internal/ui"
)

// parsePublishSpecs parses "docker run -p"-style publish specs into Docker's
// ExposedPorts + PortBindings. Defaults the host IP to 127.0.0.1 (not 0.0.0.0)
// so OAuth callbacks stay loopback-only instead of being exposed to the LAN.
func parsePublishSpecs(specs []string) (nat.PortSet, nat.PortMap, error) {
	if len(specs) == 0 {
		return nil, nil, nil
	}
	exposed := nat.PortSet{}
	bindings := nat.PortMap{}
	for _, spec := range specs {
		mappings, err := nat.ParsePortSpec(spec)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid --publish %q: %w", spec, err)
		}
		for _, m := range mappings {
			exposed[m.Port] = struct{}{}
			b := m.Binding
			if b.HostIP == "" {
				b.HostIP = "127.0.0.1"
			}
			bindings[m.Port] = append(bindings[m.Port], b)
		}
	}
	return exposed, bindings, nil
}

// missingPublishPorts returns the wanted ports that the existing container was
// not created with. PortBindings are fixed at create time, so "--publish" on a
// reused container is a silent no-op for any port not in this list.
func missingPublishPorts(inspect container.InspectResponse, wanted nat.PortMap) []string {
	if inspect.ContainerJSONBase == nil || inspect.HostConfig == nil {
		return nil
	}
	current := inspect.HostConfig.PortBindings
	var missing []string
	for port := range wanted {
		if _, ok := current[port]; !ok {
			missing = append(missing, string(port))
		}
	}
	return missing
}

func warnMissingPublish(inspect container.InspectResponse, wanted nat.PortMap) {
	if msg := formatPublishMismatch(inspect, wanted); msg != "" {
		ui.Warning(msg)
	}
}

// formatPublishMismatch builds the warning string emitted when a reused
// container does not have every port the user asked for. Returns "" when
// every wanted port is already bound on the existing container, signalling
// the caller to stay quiet. Extracted from warnMissingPublish so tests can
// pin the message format without intercepting stderr.
func formatPublishMismatch(inspect container.InspectResponse, wanted nat.PortMap) string {
	missing := missingPublishPorts(inspect, wanted)
	if len(missing) == 0 {
		return ""
	}
	sort.Strings(missing)

	wantedPorts := make([]string, 0, len(wanted))
	for port := range wanted {
		wantedPorts = append(wantedPorts, string(port))
	}
	sort.Strings(wantedPorts)

	actual := []string{}
	if inspect.HostConfig != nil {
		for port := range inspect.HostConfig.PortBindings {
			actual = append(actual, string(port))
		}
		sort.Strings(actual)
	}
	actualMsg := "none"
	if len(actual) > 0 {
		actualMsg = strings.Join(actual, ", ")
	}

	return fmt.Sprintf(
		"publish mismatch on existing container: wanted [%s], container has [%s], missing [%s] — run 'toolbox stop' then retry to apply",
		strings.Join(wantedPorts, ", "), actualMsg, strings.Join(missing, ", "),
	)
}
