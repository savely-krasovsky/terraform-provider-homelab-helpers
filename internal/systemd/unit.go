// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package systemd

import (
	"fmt"
	"regexp"
	"strings"
)

var managedUnit = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.@-]*\.(service|timer|socket|target)$`)

func ValidateUnit(name string) error {
	if !managedUnit.MatchString(name) || strings.Contains(name, "@.") {
		return fmt.Errorf("invalid managed unit: %q; expected a concrete service, timer, socket or target", name)
	}

	return nil
}
