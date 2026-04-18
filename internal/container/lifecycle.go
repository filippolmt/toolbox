package container

import (
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"

	"github.com/filippolmt/toolbox/internal/config"
	"github.com/filippolmt/toolbox/internal/mount"
	"github.com/filippolmt/toolbox/internal/ui"
)

// ContainerName e' il nome fisso del container toolbox (D-03).
// Non configurabile: un solo container per host, sempre con questo nome.
const ContainerName = "toolbox"

// execShellFn e' la funzione che attacca la shell al container.
// Variabile package-level per permettere sostituzione nei test.
var execShellFn = execShell

// NewClient crea un Docker client configurato dall'ambiente.
func NewClient() (client.APIClient, error) {
	return client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
}

// Shell gestisce il ciclo di vita del container e attacca una sessione bash.
// State machine:
//   - running  -> exec diretto (nessun container creato)
//   - stopped  -> start + exec
//   - not found -> verifica immagine, create + start + exec
func Shell(ctx context.Context, cli client.APIClient, cfg *config.Config) error {
	// Risolvere mount (D-09: path mancanti producono warning, non errori)
	binds, warnings := mount.ResolveMounts(cfg.Mounts)
	for _, w := range warnings {
		ui.Warning("mount skipped: " + w)
	}

	inspect, err := cli.ContainerInspect(ctx, ContainerName)

	switch {
	case err == nil && inspect.State.Running:
		// Container gia' running: exec diretto
		ui.Info("Connecting to running container...")
		return execShellFn(ctx, cli, inspect.ID)

	case err == nil && !inspect.State.Running:
		// Container esiste ma fermo: start + exec
		ui.Info("Starting stopped container...")
		if startErr := cli.ContainerStart(ctx, inspect.ID, container.StartOptions{}); startErr != nil {
			return fmt.Errorf("failed to start container: %w", startErr)
		}
		return execShellFn(ctx, cli, inspect.ID)

	case errdefs.IsNotFound(err):
		// Container non esiste: verificare immagine, creare, avviare
		_, err := cli.ImageInspect(ctx, cfg.ImageRef())
		if err != nil {
			return fmt.Errorf("image %q not found locally, run 'toolbox build' first", cfg.ImageRef())
		}

		ui.Info("Creating container...")
		resp, createErr := cli.ContainerCreate(ctx,
			&container.Config{
				Image:     cfg.ImageRef(),
				Tty:       true,
				OpenStdin: true,
				Cmd:       []string{"/bin/bash"},
			},
			&container.HostConfig{
				Binds: binds,
			},
			nil, // network config
			nil, // platform
			ContainerName,
		)
		if createErr != nil {
			return fmt.Errorf("failed to create container: %w", createErr)
		}

		if startErr := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); startErr != nil {
			return fmt.Errorf("failed to start container: %w", startErr)
		}
		ui.Success("Container started")
		return execShellFn(ctx, cli, resp.ID)

	default:
		return fmt.Errorf("failed to inspect container: %w", err)
	}
}

// Stop ferma e rimuove il container toolbox (D-02).
// Force remove per evitare container zombie (Pitfall 5).
func Stop(ctx context.Context, cli client.APIClient) error {
	timeout := 10
	stopErr := cli.ContainerStop(ctx, ContainerName, container.StopOptions{Timeout: &timeout})

	if errdefs.IsNotFound(stopErr) {
		ui.Warning("No running container found")
		return nil
	}
	if stopErr != nil {
		return fmt.Errorf("failed to stop container: %w", stopErr)
	}

	rmErr := cli.ContainerRemove(ctx, ContainerName, container.RemoveOptions{Force: true})
	if rmErr != nil && !errdefs.IsNotFound(rmErr) {
		return fmt.Errorf("failed to remove container: %w", rmErr)
	}

	ui.Success("Container stopped and removed")
	return nil
}
