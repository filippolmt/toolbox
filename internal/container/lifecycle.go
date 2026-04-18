package container

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/filippolmt/toolbox/internal/ui"
)

// ContainerName e' il nome fisso del container toolbox.
const ContainerName = "toolbox"

// NewClient crea un nuovo Docker client con negoziazione automatica della versione API.
func NewClient() (client.APIClient, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating Docker client: %w", err)
	}
	return cli, nil
}

// Stop ferma e rimuove il container toolbox (D-02: stop + remove).
func Stop(ctx context.Context, cli client.APIClient) error {
	ui.Info("Stopping container " + ContainerName + "...")

	// Ferma il container (timeout default del daemon)
	if err := cli.ContainerStop(ctx, ContainerName, container.StopOptions{}); err != nil {
		// Se il container non esiste o e' gia' fermo, non e' un errore fatale
		ui.Warning("Container stop: " + err.Error())
	}

	// Rimuovi il container (force per gestire stati intermedi)
	if err := cli.ContainerRemove(ctx, ContainerName, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("removing container: %w", err)
	}

	ui.Success("Container " + ContainerName + " stopped and removed")
	return nil
}
