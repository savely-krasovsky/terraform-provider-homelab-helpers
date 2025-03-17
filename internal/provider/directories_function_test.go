// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestDirectoriesFunction_Known(t *testing.T) {
	if err := os.Mkdir("example1", 0o744); err != nil && !errors.Is(err, fs.ErrExist) {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove("example1")
	}()
	if err := os.Mkdir("example1/example2", 0o744); err != nil && !errors.Is(err, fs.ErrExist) {
		t.Fatal(err)
	}
	defer func() {
		_ = os.RemoveAll("example1/example2")
	}()
	f, err := os.Create("example1/example2/test.txt")
	if err != nil && !errors.Is(err, fs.ErrExist) {
		t.Fatal(err)
	}
	defer func() {
		_ = f.Close()
	}()
	if err := os.Mkdir("example3", 0o744); err != nil && !errors.Is(err, fs.ErrExist) {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Remove("example3")
	}()

	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
				output "test" {
					value = provider::homelab-helpers::directories("${path.module}", true)
				}
				`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownOutputValue(
						"test",
						knownvalue.ListExact([]knownvalue.Check{
							knownvalue.StringExact("example1"),
							knownvalue.StringExact("example1/example2"),
							knownvalue.StringExact("example3"),
						}),
					),
				},
			},
		},
	})
}
