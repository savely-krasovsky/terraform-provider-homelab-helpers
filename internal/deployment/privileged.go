// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/remote"
)

func (e Engine) rootTemp(ctx context.Context, dir string) (string, error) {
	output, err := e.Host.Run(ctx, remote.Command{
		Name: "sudo",
		Args: []string{"-n", "mktemp", path.Join(dir, ".homelab-install.XXXXXX")},
	})
	if err != nil {
		return "", err
	}

	tmp := strings.TrimSpace(string(output))
	if path.Dir(tmp) != dir || !strings.HasPrefix(path.Base(tmp), ".homelab-install.") {
		return "", fmt.Errorf("mktemp returned an invalid destination")
	}

	return tmp, nil
}

func (e Engine) removeRootTemp(ctx context.Context, tmp string) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	_ = sudo(cleanup, e.Host, "rm", "-f", "--", tmp)
}

func (e Engine) applyFirewall(ctx context.Context, source string) error {
	destination := e.Paths.Firewall
	if err := sudo(ctx, e.Host, "install", "-d", "-m", "0755", path.Dir(destination)); err != nil {
		return err
	}

	tmp, err := e.rootTemp(ctx, path.Dir(destination))
	if err != nil {
		return err
	}
	defer e.removeRootTemp(ctx, tmp)

	if err := sudo(ctx, e.Host, "install", "-m", "0644", source, tmp); err != nil {
		return err
	}

	if err := sudo(ctx, e.Host, "nft", "--file", tmp); err != nil {
		return err
	}

	return e.commitRootFile(ctx, tmp, destination)
}

func (e Engine) commitRootFile(ctx context.Context, tmp, destination string) error {
	if err := sudo(ctx, e.Host, "sync", "-f", tmp); err != nil {
		return err
	}

	if err := sudo(ctx, e.Host, "mv", "-f", "--", tmp, destination); err != nil {
		return err
	}

	if err := sudo(ctx, e.Host, "restorecon", destination); err != nil {
		return err
	}

	return sudo(ctx, e.Host, "sync", "-f", path.Dir(destination))
}
