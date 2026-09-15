// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package host

import (
	"fmt"
	"strings"
)

const stderrLimit = 16 << 10

// Stderr retains the beginning of command diagnostics while draining all output.
// The zero value is ready to use; secret-bearing commands must not write to it.
type Stderr struct {
	data      []byte
	truncated bool
}

func (s *Stderr) Write(data []byte) (int, error) {
	keep := min(len(data), stderrLimit-len(s.data))
	s.data = append(s.data, data[:keep]...)
	s.truncated = s.truncated || keep < len(data)

	return len(data), nil
}

func (s *Stderr) Wrap(err error) error {
	detail := strings.TrimSpace(string(s.data))
	if s.truncated {
		detail += "\n[stderr truncated]"
	}

	if detail == "" {
		return err
	}

	return fmt.Errorf("%w: %s", err, detail)
}
