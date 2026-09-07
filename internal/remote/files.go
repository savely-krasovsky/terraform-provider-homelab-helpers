// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package remote

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// CheckPath refuses symlinks in every existing component. The managed tree must
// belong to the SSH user; it must not be writable by other users.
func (c *Client) CheckPath(name string) error {
	if !path.IsAbs(name) || path.Clean(name) != name || name == "/" {
		return fmt.Errorf("expected a clean absolute remote path: %q", name)
	}

	current := "/"
	for part := range strings.SplitSeq(strings.TrimPrefix(name, "/"), "/") {
		current = path.Join(current, part)
		info, err := c.fs.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect remote path %s: %w", current, err)
		}

		if info.Mode()&fs.ModeSymlink != 0 || (current != name && !info.IsDir()) {
			return fmt.Errorf("remote path contains a symlink or non-directory: %s", current)
		}
	}

	return nil
}

func (c *Client) Stat(name string) (fs.FileInfo, error) {
	if err := c.CheckPath(name); err != nil {
		return nil, err
	}

	return c.fs.Lstat(name)
}

func (c *Client) ReadFile(name string) ([]byte, error) {
	info, err := c.Stat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular remote file: %s", name)
	}

	file, err := c.fs.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(file)
}

func (c *Client) Mkdir(name string, mode fs.FileMode) error {
	if err := c.CheckPath(name); err != nil {
		return err
	}

	if err := c.fs.MkdirAll(name); err != nil {
		return err
	}

	return c.fs.Chmod(name, mode)
}

// Upload creates a staging file. It must not replace a live configuration file.
func (c *Client) Upload(name string, data []byte, mode fs.FileMode) error {
	if err := c.CheckPath(name); err != nil {
		return err
	}

	if err := c.fs.MkdirAll(path.Dir(name)); err != nil {
		return err
	}

	file, err := c.fs.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()

		return err
	}

	_, writeErr := file.Write(data)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func (c *Client) Remove(name string) error {
	if err := c.CheckPath(name); err != nil {
		return err
	}

	err := c.fs.Remove(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	return err
}

func (c *Client) RemoveStage(name string) error {
	if !strings.HasPrefix(path.Base(name), ".homelab-stage-") {
		return errors.New("refusing to remove a non-staging directory")
	}

	if err := c.CheckPath(name); err != nil {
		return err
	}

	return c.fs.RemoveAll(name)
}
