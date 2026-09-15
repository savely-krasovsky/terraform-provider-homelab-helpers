// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package systemd

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/host"
)

type Directories struct{ Config, State string }

// UserDirectories asks the host for its XDG directories, which is where Quadlet and
// the user manager look and where the deployment keeps its ownership record.
func UserDirectories(ctx context.Context, h host.Runner) (Directories, error) {
	output, err := h.Run(ctx, host.Command{Name: "systemd-path", Args: []string{"user-configuration", "user-state-private"}})
	if err != nil {
		return Directories{}, err
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 2 {
		return Directories{}, fmt.Errorf("unexpected systemd-path output: %q", output)
	}

	paths := Directories{Config: lines[0], State: lines[1]}
	for _, name := range []string{paths.Config, paths.State} {
		if !path.IsAbs(name) || path.Clean(name) != name || name == "/" || strings.ContainsAny(name, "\x00\r\n") {
			return Directories{}, fmt.Errorf("expected a clean absolute path from systemd-path: %q", name)
		}
	}

	return paths, nil
}
