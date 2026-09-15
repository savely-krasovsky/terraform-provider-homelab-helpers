// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/stretchr/testify/require"
)

var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"quadlet": providerserver.NewProtocol6WithError(New("test")()),
}

func TestAccProviderPlan(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			PlanOnly: true, ExpectNonEmptyPlan: true,
			Config: `
provider "quadlet" {
  host = "192.0.2.10"
  user = "deploy"
}

variable "password" {
  type      = string
  sensitive = true
  ephemeral = true
  default   = "acceptance-password"
}

resource "quadlet_podman_secret" "test" {
  name     = "acceptance-password"
  value_wo = var.password
}

resource "quadlet_deployment" "test" {
  name = "acceptance"
  files = {
    "systemd/user/app.service" = "[Service]\nExecStart=/bin/true\n"
  }
  restart = ["app.service"]
  triggers = {
    password   = quadlet_podman_secret.test.revision
  }
}
`,
		}},
	})
}

func TestAccConfigLifecycle(t *testing.T) {
	engine, _ := lifecycleEngine(t)
	provider := lifecycleProvider{Provider: New("test")(), access: lifecycleAccess{target: engine}}
	config := func(value string) string {
		return fmt.Sprintf(`
provider "quadlet" {
  host = "192.0.2.10"
  user = "deploy"
}

resource "quadlet_deployment" "test" {
  name  = "acceptance"
  files = { "app/settings.conf" = %q }
}
`, value)
	}
	step := func(value string) resource.TestStep {
		return resource.TestStep{
			Config: config(value),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("quadlet_deployment.test", "files.app/settings.conf", value),
				resource.TestCheckResourceAttr("quadlet_deployment.test", "units.#", "0"),
				resource.TestCheckResourceAttrSet("quadlet_deployment.test", "id"),
				resource.TestCheckResourceAttrSet("quadlet_deployment.test", "applied_revision"),
			),
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
			PostApplyFunc: func() {
				data, err := os.ReadFile(filepath.Join(engine.Paths.Config, "app/settings.conf"))
				require.NoError(t, err)
				require.Equal(t, value, string(data))
			},
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"quadlet": providerserver.NewProtocol6WithError(provider),
		},
		CheckDestroy: func(_ *terraform.State) error {
			_, err := os.Stat(filepath.Join(engine.Paths.Config, "app/settings.conf"))
			if !os.IsNotExist(err) {
				return fmt.Errorf("expected deployment file to be removed, got: %v", err)
			}

			return nil
		},
		Steps: []resource.TestStep{
			step("first"),
			{
				ResourceName: "quadlet_deployment.test", ImportState: true,
				ImportStateId: "acceptance", ImportStateVerify: true,
			},
			step("second"),
		},
	})
}

func TestAccSecretLifecycle(t *testing.T) {
	client := &memorySecret{}
	provider := secretLifecycleProvider{Provider: New("test")(), client: client}
	address := "quadlet_podman_secret.test"
	step := func(version, value string) resource.TestStep {
		return resource.TestStep{
			Config: fmt.Sprintf(`
provider "quadlet" {
  host = "192.0.2.10"
  user = "deploy"
}

variable "password" {
  type      = string
  sensitive = true
  ephemeral = true
  default   = %q
}

resource "quadlet_podman_secret" "test" {
  name     = "acceptance-password"
  value_wo = var.password
  version  = %q
}
`, value, version),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet(address, "id"),
				resource.TestCheckResourceAttr(address, "version", version),
				resource.TestCheckResourceAttrSet(address, "revision"),
				resource.TestCheckNoResourceAttr(address, "value_wo"),
			),
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
			PostApplyFunc: func() {
				require.True(t, client.present)
				require.Equal(t, value, client.value)
			},
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: map[string]func() (tfprotov6.ProviderServer, error){
			"quadlet": providerserver.NewProtocol6WithError(provider),
		},
		CheckDestroy: func(_ *terraform.State) error {
			if client.present {
				return fmt.Errorf("secret still exists after destroy")
			}

			return nil
		},
		Steps: []resource.TestStep{
			step("1", "first-password"),
			{ResourceName: address, ImportState: true, ImportStateId: "acceptance-password", ImportStateVerify: true},
			step("2", "rotated-password"),
		},
	})
}
