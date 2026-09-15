// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

//go:build !linux

package local

import "os/exec"

func configureCommand(*exec.Cmd) {}
