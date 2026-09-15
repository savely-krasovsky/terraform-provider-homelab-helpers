// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"context"
	"sync"
	"time"

	"github.com/gofrs/flock"
)

// Lock uses the same kernel flock as SSH, so both transports share the lease.
func (c *Client) Lock(ctx context.Context, name string) (context.Context, func(), error) {
	if err := c.CheckPath(name); err != nil {
		return nil, nil, err
	}

	file := flock.New(name, flock.SetPermissions(0600))
	lease, cancel := context.WithCancel(ctx)
	stopClient := context.AfterFunc(c.ctx, cancel)
	release := sync.OnceFunc(func() {
		cancel()
		stopClient()
		// Drain local filesystem calls before another operation gets the lock.
		c.Close()

		_ = file.Close()
	})

	locked, err := file.TryLockContext(lease, 25*time.Millisecond)
	if err != nil {
		release()
		return nil, nil, err
	}

	if !locked {
		release()
		return nil, nil, context.Canceled
	}

	stopLease := context.AfterFunc(lease, release)

	return lease, func() { stopLease(); release() }, nil
}
