// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func checkPath(name string) error {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name || name == "/" {
		return fmt.Errorf("expected a clean absolute local path: %q", name)
	}

	current := "/"
	for part := range strings.SplitSeq(strings.TrimPrefix(name, "/"), "/") {
		current = filepath.Join(current, part)

		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		if err != nil {
			return err
		}

		if info.Mode()&fs.ModeSymlink != 0 || (current != name && !info.IsDir()) {
			return fmt.Errorf("local path contains a symlink or non-directory: %s", current)
		}
	}

	return nil
}

func (c *Client) CheckPath(name string) error {
	done, err := c.begin()
	if err != nil {
		return err
	}
	defer done()

	return checkPath(name)
}

func (c *Client) Stat(name string) (fs.FileInfo, error) {
	done, err := c.begin()
	if err != nil {
		return nil, err
	}
	defer done()

	// Read-only metadata discovery follows links, including generator binaries
	// and generated unit aliases. Managed reads and writes check their paths.
	return os.Stat(name)
}

func (c *Client) ReadFile(name string) ([]byte, error) {
	done, err := c.begin()
	if err != nil {
		return nil, err
	}
	defer done()

	if err := regularFile(name); err != nil {
		return nil, err
	}

	return os.ReadFile(name)
}

func regularFile(name string) error {
	if err := checkPath(name); err != nil {
		return err
	}

	info, err := os.Lstat(name)
	if err != nil {
		return err
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular local file: %s", name)
	}

	return nil
}

func (c *Client) ReadDir(name string) ([]string, error) {
	done, err := c.begin()
	if err != nil {
		return nil, err
	}
	defer done()

	if err := checkPath(name); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(name)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names, nil
}

func (c *Client) Mkdir(name string, mode fs.FileMode) error {
	done, err := c.begin()
	if err != nil {
		return err
	}
	defer done()

	if err := checkPath(name); err != nil {
		return err
	}

	if err := os.MkdirAll(name, mode); err != nil {
		return err
	}

	return os.Chmod(name, mode)
}

func (c *Client) Upload(name string, data []byte, mode fs.FileMode) error {
	done, err := c.begin()
	if err != nil {
		return err
	}
	defer done()

	return upload(name, data, mode)
}

func upload(name string, data []byte, mode fs.FileMode) error {
	if err := checkPath(name); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return err
	}

	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}

	_, writeErr := file.Write(data)

	return errors.Join(writeErr, file.Chmod(mode), file.Close())
}

func (c *Client) WriteFile(ctx context.Context, name string, data []byte, mode fs.FileMode) error {
	done, err := c.begin()
	if err != nil {
		return err
	}
	defer done()

	if err := ctx.Err(); err != nil {
		return err
	}

	tmp := filepath.Join(filepath.Dir(name), ".terraform-write-"+rand.Text())
	defer func() { _ = os.Remove(tmp) }()

	if err := upload(tmp, data, mode); err != nil {
		return err
	}

	return c.commit(ctx, tmp, name)
}

func (c *Client) InstallFile(ctx context.Context, source, destination string) error {
	done, err := c.begin()
	if err != nil {
		return err
	}
	defer done()

	if err := ctx.Err(); err != nil {
		return err
	}

	if err := regularFile(source); err != nil {
		return err
	}

	if err := checkPath(destination); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}

	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()

	tmp := filepath.Join(filepath.Dir(destination), ".terraform-write-"+rand.Text())
	defer func() { _ = os.Remove(tmp) }()

	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(file, src)
	if err := errors.Join(copyErr, file.Chmod(0644), file.Close()); err != nil {
		return err
	}

	return c.commit(ctx, tmp, destination)
}

func (c *Client) commit(ctx context.Context, tmp, destination string) error {
	if err := checkPath(destination); err != nil {
		return err
	}

	if err := syncPath(tmp); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	if err := c.ctx.Err(); err != nil {
		return err
	}

	if err := os.Rename(tmp, destination); err != nil {
		return err
	}

	return syncPath(filepath.Dir(destination))
}

func syncPath(name string) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}

	return errors.Join(file.Sync(), file.Close())
}

func (c *Client) Sync(ctx context.Context, name string) error {
	done, err := c.begin()
	if err != nil {
		return err
	}
	defer done()

	if err := ctx.Err(); err != nil {
		return err
	}

	if err := checkPath(name); err != nil {
		return err
	}

	return syncPath(name)
}

func (c *Client) Remove(name string) error {
	done, err := c.begin()
	if err != nil {
		return err
	}
	defer done()

	if err := checkPath(name); err != nil {
		return err
	}

	err = os.Remove(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	return err
}

func (c *Client) RemoveStage(name string) error {
	done, err := c.begin()
	if err != nil {
		return err
	}
	defer done()

	if !strings.HasPrefix(filepath.Base(name), ".terraform-stage-") {
		return errors.New("refusing to remove a non-staging directory")
	}

	if err := checkPath(name); err != nil {
		return err
	}

	return os.RemoveAll(name)
}
