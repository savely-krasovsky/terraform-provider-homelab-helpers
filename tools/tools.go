// Copyright (c) HashiCorp, Inc.
// Copyright (c) 2025, 2026 Savely Krasovsky
// SPDX-License-Identifier: MPL-2.0

//go:build generate

package tools

// Generate copyright headers
//go:generate go tool copywrite headers -d .. --config ../.copywrite.hcl

// Format examples used in the documentation.
//go:generate terraform fmt -recursive ../examples/

// Generate documentation.
//go:generate go tool tfplugindocs generate --provider-dir .. -provider-name homelab
