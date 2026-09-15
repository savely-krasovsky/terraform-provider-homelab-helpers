// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

// Package host defines the I/O contracts implemented by local and SSH sessions.
package host

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"strconv"
	"strings"
)

type Command struct {
	Name  string
	Args  []string
	Env   []string
	Stdin io.Reader
	// CaptureStderr includes bounded diagnostics in errors. Enable only for
	// commands whose output cannot contain secret values.
	CaptureStderr bool
}

type Runner interface {
	Run(context.Context, Command) ([]byte, error)
}

type Dialer func(context.Context, string, string) (net.Conn, error)

// Files operates within a session's lifetime. Close cancels outstanding remote
// I/O; local filesystem calls finish before Close returns. Upload is exclusive,
// WriteFile and InstallFile atomically replace one file, and Sync is a durability
// barrier. Managed paths must be absolute and contain no symlinks. Stat is a
// read-only metadata probe that follows links (e.g. generated unit aliases);
// it does not validate a path for managed file operations.
type Files interface {
	Stat(string) (fs.FileInfo, error)
	ReadFile(string) ([]byte, error)
	ReadDir(string) ([]string, error)
	Mkdir(string, fs.FileMode) error
	Upload(string, []byte, fs.FileMode) error
	WriteFile(context.Context, string, []byte, fs.FileMode) error
	InstallFile(context.Context, string, string) error
	Remove(string) error
	RemoveStage(string) error
	CheckPath(string) error
	Sync(context.Context, string) error
}

// Session belongs to one provider operation. Lock releases or loses the lease
// by cancelling its context and closing the session; a session must not be shared
// with another resource. Implementations acquire file services lazily.
type Session interface {
	Files
	Runner
	DialContext(context.Context, string, string) (net.Conn, error)
	Lock(context.Context, string) (context.Context, func(), error)
	Close()
}

type Open func(context.Context) (Session, error)

func UserID(ctx context.Context, runner Runner) (int, error) {
	output, err := runner.Run(ctx, Command{Name: "id", Args: []string{"-u"}})
	if err != nil {
		return 0, err
	}

	uid, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || uid < 0 {
		return 0, fmt.Errorf("unexpected uid: %q", output)
	}

	return uid, nil
}
