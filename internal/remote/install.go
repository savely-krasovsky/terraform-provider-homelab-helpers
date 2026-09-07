// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package remote

import (
	"context"
	"crypto/rand"
	"fmt"
	"io/fs"
	"path"
)

func (c *Client) WriteFile(ctx context.Context, name string, data []byte, mode fs.FileMode) error {
	tmp := path.Join(path.Dir(name), ".homelab-write-"+rand.Text())
	defer func() { _ = c.fs.Remove(tmp) }()

	if err := c.Upload(tmp, data, mode); err != nil {
		return err
	}

	return c.commitFile(ctx, tmp, name)
}

// InstallFile copies a validated configuration on the host. A temporary file
// beside the destination keeps replacement atomic even across mount points and
// gives the file the destination directory's SELinux context.
func (c *Client) InstallFile(ctx context.Context, source, destination string) error {
	if err := c.CheckPath(source); err != nil {
		return err
	}
	if err := c.CheckPath(destination); err != nil {
		return err
	}
	if err := c.fs.MkdirAll(path.Dir(destination)); err != nil {
		return err
	}

	tmp := path.Join(path.Dir(destination), ".homelab-write-"+rand.Text())
	defer func() { _ = c.fs.Remove(tmp) }()

	if _, err := c.Run(ctx, Command{Name: "install", Args: []string{"-m", "0644", "--", source, tmp}}); err != nil {
		return err
	}

	return c.commitFile(ctx, tmp, destination)
}

func (c *Client) commitFile(ctx context.Context, tmp, destination string) error {
	if err := c.CheckPath(destination); err != nil {
		return err
	}

	if _, err := c.Run(ctx, Command{Name: "sync", Args: []string{"-f", tmp}}); err != nil {
		return err
	}

	// OpenSSH SFTP exposes atomic overwrite through the POSIX rename extension.
	if err := c.fs.PosixRename(tmp, destination); err != nil {
		return fmt.Errorf("atomic SFTP rename (requires posix-rename@openssh.com): %w", err)
	}

	_, err := c.Run(ctx, Command{Name: "sync", Args: []string{"-f", path.Dir(destination)}})

	return err
}
