package orca

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// shellExec is the part of the Docker client Offer needs.
type shellExec interface {
	ExecCreate(ctx context.Context, container string, o client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecAttach(ctx context.Context, execID string, o client.ExecAttachOptions) (client.ExecAttachResult, error)
}

// offerCmd asks the running server for a pairing offer (`orca pair`): an
// unused offer is reused, a used one replaced. The client dials host
// loopback, where the session plan publishes the port; without --address
// Orca would advertise the container's own address.
var offerCmd = []string{"sh", "-c", `exec "$ORCA_USER_DATA_PATH/server/current/bin/orca-ide" pair --address 127.0.0.1 --json`}

// Offer returns a pairing link from the Orca server running in the Orca
// Shell named container.
func Offer(ctx context.Context, cli shellExec, container string) (string, error) {
	created, err := cli.ExecCreate(ctx, container, client.ExecCreateOptions{AttachStdout: true, AttachStderr: true, Cmd: offerCmd})
	if err != nil {
		return "", err
	}
	att, err := cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return "", err
	}
	defer att.Close()
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, att.Reader); err != nil {
		return "", err
	}
	var res struct {
		Result *struct {
			Available  bool   `json:"available"`
			PairingURL string `json:"pairingUrl"`
			Reason     string `json:"reason"`
			Guidance   string `json:"guidance"`
		} `json:"result"`
	}
	if json.Unmarshal(stdout.Bytes(), &res) != nil || res.Result == nil {
		return "", fmt.Errorf("orca pair printed no offer: %s", strings.TrimSpace(stderr.String()+" "+stdout.String()))
	}
	if !res.Result.Available {
		return "", fmt.Errorf("pairing is unavailable (%s): %s", res.Result.Reason, res.Result.Guidance)
	}
	return res.Result.PairingURL, nil
}
