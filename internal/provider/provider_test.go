// Copyright (c) HashiCorp, Inc.
// Copyright (c) 2025, 2026 Savely Krasovsky
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/stretchr/testify/require"
)

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"homelab": providerserver.NewProtocol6WithError(New("test")()),
}

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(root, "example1/example2"), 0755))
	require.NoError(t, os.Mkdir(filepath.Join(root, "example3"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "example1/example2/test.txt"), nil, 0644))

	return filepath.ToSlash(root)
}

func TestProviderSchema(t *testing.T) {
	server := providerserver.NewProtocol6(New("test")())()

	response, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, err)
	require.Empty(t, response.Diagnostics)

	require.Len(t, response.ResourceSchemas, 1)
	require.Contains(t, response.ResourceSchemas, "homelab_config")
	require.Len(t, response.Functions, 2)
	require.Contains(t, response.Functions, "dirset")
	require.Contains(t, response.Functions, "dirhash")

	// Scaffolding extension points must not expose its sample objects.
	require.Empty(t, response.DataSourceSchemas)
	require.Empty(t, response.EphemeralResourceSchemas)
	require.Empty(t, response.ActionSchemas)
}
