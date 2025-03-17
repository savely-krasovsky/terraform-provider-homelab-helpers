// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// testAccProtoV6ProviderFactories is used to instantiate a provider during acceptance testing.
// The factory function is called for each Terraform CLI command to create a provider
// server that the CLI can connect to and interact with.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"homelab-helpers": providerserver.NewProtocol6WithError(New("test")()),
}

func TestMain(m *testing.M) {
	if err := os.Mkdir("example1", 0o744); err != nil && !errors.Is(err, fs.ErrExist) {
		log.Fatal(err)
	}
	defer func() {
		_ = os.Remove("example1")
	}()
	if err := os.Mkdir("example1/example2", 0o744); err != nil && !errors.Is(err, fs.ErrExist) {
		log.Fatal(err)
	}
	defer func() {
		_ = os.RemoveAll("example1/example2")
	}()
	f, err := os.Create("example1/example2/test.txt")
	if err != nil && !errors.Is(err, fs.ErrExist) {
		log.Fatal(err)
	}
	defer func() {
		_ = f.Close()
	}()
	if err := os.Mkdir("example3", 0o744); err != nil && !errors.Is(err, fs.ErrExist) {
		log.Fatal(err)
	}
	defer func() {
		_ = os.Remove("example3")
	}()

	m.Run()
}
