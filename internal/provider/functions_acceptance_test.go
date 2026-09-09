// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
)

func TestAccFunctions(t *testing.T) {
	root := fixture(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
    output "hash" { value = provider::homelab::dirhash(%q, "example1/**") }
    output "directories" { value = provider::homelab::dirset(%q, "**") }
   `, root, root),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownOutputValue("hash", knownvalue.StringExact("f0c5942360b2f167c8b99b3771f6f22a3b7c4e623046586b2f5aee730c4d1e31")),
				statecheck.ExpectKnownOutputValue("directories", knownvalue.ListExact([]knownvalue.Check{
					knownvalue.StringExact("example1"),
					knownvalue.StringExact("example1/example2"),
					knownvalue.StringExact("example3"),
				})),
			},
		}},
	})
}

func TestAccHomelabConfigPlan(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
			Config: `
    variable "password" {
     type = string
     sensitive = true
     ephemeral = true
     default = "test-password"
    }

    resource "homelab_config" "test" {
     host = "192.0.2.10"
     files = {}
     units = []
     groups = {}
     firewall = "table inet filter {}"
     secrets = { app_password = "test-reference" }
     secret_values_wo = { app_password = var.password }
    }
   `,
		}},
	})
}
