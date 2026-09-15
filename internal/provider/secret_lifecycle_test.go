// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"crypto/rand"
	"errors"
	"github.com/savely-krasovsky/terraform-provider-quadlet/internal/podman"
	"testing"

	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"
)

type memorySecret struct {
	present      bool
	fail         bool
	loseResponse bool
	value        string
	secret       podman.Secret
	beforeRemove func()
}

func (s *memorySecret) Create(_ context.Context, name, owner, version, value string) (string, error) {
	if s.fail {
		return "", errors.New("installation failed")
	}

	if s.present {
		return "", errors.New("secret name in use")
	}

	s.secret.ID = rand.Text()
	s.secret.Spec.Name = name
	s.secret.Spec.Labels = map[string]string{podman.OwnerLabel: owner, podman.VersionLabel: version}

	s.present, s.value = true, value
	if s.loseResponse {
		return "", errors.New("connection lost after creation")
	}

	return s.secret.ID, nil
}
func (s *memorySecret) Inspect(context.Context, string) (*podman.Secret, error) {
	if !s.present {
		return nil, nil
	}

	secret := s.secret

	return &secret, nil
}
func (s *memorySecret) Remove(_ context.Context, id string) error {
	if s.beforeRemove != nil {
		s.beforeRemove()

		s.beforeRemove = nil
	}

	if s.secret.ID == id {
		s.present = false
	}

	return nil
}
func (s *memorySecret) secrets(ctx context.Context, fn func(context.Context, secretClient) error) error {
	return fn(ctx, s)
}

type secretLifecycleProvider struct {
	frameworkprovider.Provider
	client *memorySecret
}

func (p secretLifecycleProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{func() resource.Resource { return &secretResource{access: p.client} }}
}

func (p secretLifecycleProvider) Configure(ctx context.Context, req frameworkprovider.ConfigureRequest, resp *frameworkprovider.ConfigureResponse) {
	p.Provider.Configure(ctx, req, resp)

	resp.ResourceData = p.client
}

func TestSecretOwnershipAndRecovery(t *testing.T) {
	client := &memorySecret{}
	server := providerserver.NewProtocol6(secretLifecycleProvider{Provider: New("test")(), client: client})()
	schemas, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, err)

	name := "quadlet_podman_secret"
	schema := schemas.ResourceSchemas[name]
	typ, config := object(t, schema, map[string]any{"name": "password", "version": "1", "value_wo": "first"})
	absent := nullOf(t, typ)
	apply := func(state, config *tfprotov6.DynamicValue) *tfprotov6.ApplyResourceChangeResponse {
		planned, err := server.PlanResourceChange(t.Context(), &tfprotov6.PlanResourceChangeRequest{
			TypeName: name, PriorState: state, ProposedNewState: config, Config: config,
		})
		require.NoError(t, err)
		require.Empty(t, planned.Diagnostics)

		result, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
			TypeName: name, PriorState: state, PlannedState: planned.PlannedState, Config: config,
		})
		require.NoError(t, err)

		return result
	}
	destroy := func(state *tfprotov6.DynamicValue) {
		result, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
			TypeName: name, PriorState: state, PlannedState: absent, Config: absent,
		})
		require.NoError(t, err)
		require.Empty(t, result.Diagnostics)
	}

	// A second resource must not claim or replace the first one's secret.
	created := apply(absent, config)
	require.Empty(t, created.Diagnostics)

	_, renamedConfig := object(t, schema, map[string]any{"name": "renamed", "version": "1", "value_wo": "first"})
	renamed, err := server.PlanResourceChange(t.Context(), &tfprotov6.PlanResourceChangeRequest{
		TypeName: name, PriorState: created.NewState, ProposedNewState: proposed(t, schema, created.NewState, renamedConfig), Config: renamedConfig,
	})
	require.NoError(t, err)
	require.Empty(t, renamed.Diagnostics)
	require.False(t, attributes(t, typ, renamed.PlannedState)["id"].IsKnown())

	originalID := client.secret.ID
	conflict := apply(absent, config)
	require.NotEmpty(t, conflict.Diagnostics)

	value, err := conflict.NewState.Unmarshal(typ)
	require.NoError(t, err)
	require.True(t, value.IsNull())
	require.Equal(t, originalID, client.secret.ID)
	require.Equal(t, "first", client.value)

	// A replacement under the same name is not ours to update or destroy.
	client.present = false
	_, err = client.Create(t.Context(), "password", "someone-else", "1", "foreign")
	require.NoError(t, err)

	foreignID := client.secret.ID
	_, updateConfig := object(t, schema, map[string]any{"name": "password", "version": "2", "value_wo": "update"})
	updated := apply(created.NewState, updateConfig)
	require.NotEmpty(t, updated.Diagnostics)
	destroy(created.NewState)
	require.True(t, client.present)
	require.Equal(t, foreignID, client.secret.ID)
	require.Equal(t, "foreign", client.value)

	// An unlabelled secret cannot be adopted implicitly or through import.
	client.secret.Spec.Labels = nil
	imported, err := server.ImportResourceState(t.Context(), &tfprotov6.ImportResourceStateRequest{TypeName: name, ID: "password"})
	require.NoError(t, err)
	require.NotEmpty(t, imported.Diagnostics)

	// Creation can succeed remotely even when Terraform receives an error.
	client.present, client.loseResponse = false, true
	uncertain := apply(absent, config)
	require.NotEmpty(t, uncertain.Diagnostics)

	state := attributes(t, typ, uncertain.NewState)

	var owner string
	require.NoError(t, state["id"].As(&owner))
	require.Equal(t, owner, client.secret.Spec.Labels[podman.OwnerLabel])
	require.True(t, state["value_wo"].IsNull())

	imported, err = server.ImportResourceState(t.Context(), &tfprotov6.ImportResourceStateRequest{TypeName: name, ID: "password"})
	require.NoError(t, err)
	require.Empty(t, imported.Diagnostics)
	require.Equal(t, state["id"], attributes(t, typ, imported.ImportedResources[0].State)["id"])
	destroy(uncertain.NewState)
	require.False(t, client.present)

	client.loseResponse = false

	// Refresh learns the committed version if an update's response is lost,
	// including when the user then restores the previous configuration version.
	created = apply(absent, config)
	require.Empty(t, created.Diagnostics)

	client.loseResponse = true
	updated = apply(created.NewState, updateConfig)
	require.NotEmpty(t, updated.Diagnostics)

	refreshed, err := server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: name, CurrentState: created.NewState})
	require.NoError(t, err)
	require.Empty(t, refreshed.Diagnostics)
	require.Equal(t, tftypes.NewValue(tftypes.String, "2"), attributes(t, typ, refreshed.NewState)["version"])
	require.Equal(t, tftypes.NewValue(tftypes.String, client.secret.ID), attributes(t, typ, refreshed.NewState)["revision"])

	client.loseResponse = false

	destroy(refreshed.NewState)

	// Reusing a name during rotation cannot turn an ID deletion into an overwrite.
	created = apply(absent, config)
	require.Empty(t, created.Diagnostics)

	client.beforeRemove = func() {
		client.present = false
		_, err := client.Create(t.Context(), "password", "concurrent-owner", "1", "concurrent-value")
		require.NoError(t, err)
	}
	updated = apply(created.NewState, updateConfig)
	require.NotEmpty(t, updated.Diagnostics)
	require.Equal(t, "concurrent-value", client.value)
	require.Equal(t, "concurrent-owner", client.secret.Spec.Labels[podman.OwnerLabel])
}

// proposed overlays configuration on prior state, as Terraform Core does for
// non-computed arguments. Write-only values never enter proposed state.
func proposed(t *testing.T, schema *tfprotov6.Schema, state, config *tfprotov6.DynamicValue) *tfprotov6.DynamicValue {
	t.Helper()

	typ := schema.ValueType()
	values := attributes(t, typ, state)

	configured := attributes(t, typ, config)
	for _, attribute := range schema.Block.Attributes {
		if !attribute.Computed || !configured[attribute.Name].IsNull() {
			values[attribute.Name] = configured[attribute.Name]
		}

		if attribute.WriteOnly {
			values[attribute.Name] = tftypes.NewValue(attribute.Type, nil)
		}
	}

	dynamic, err := tfprotov6.NewDynamicValue(typ, tftypes.NewValue(typ, values))
	require.NoError(t, err)

	return &dynamic
}

func TestSecretInstallationRevision(t *testing.T) {
	client := &memorySecret{}
	server := providerserver.NewProtocol6(secretLifecycleProvider{Provider: New("test")(), client: client})()
	schemas, err := server.GetProviderSchema(t.Context(), &tfprotov6.GetProviderSchemaRequest{})
	require.NoError(t, err)

	name := "quadlet_podman_secret"
	schema := schemas.ResourceSchemas[name]
	typ, config := object(t, schema, map[string]any{"name": "password", "version": "1", "value_wo": "private-value"})

	plan := func(state, config *tfprotov6.DynamicValue) *tfprotov6.PlanResourceChangeResponse {
		next := config
		if value, err := state.Unmarshal(typ); err == nil && !value.IsNull() {
			next = proposed(t, schema, state, config)
		}

		response, err := server.PlanResourceChange(t.Context(), &tfprotov6.PlanResourceChangeRequest{
			TypeName: name, PriorState: state, ProposedNewState: next, Config: config,
		})
		require.NoError(t, err)
		require.Empty(t, response.Diagnostics)

		return response
	}
	apply := func(state, config *tfprotov6.DynamicValue) *tfprotov6.ApplyResourceChangeResponse {
		planned := plan(state, config)
		require.False(t, attributes(t, typ, planned.PlannedState)["revision"].IsKnown())

		response, err := server.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
			TypeName: name, PriorState: state, PlannedState: planned.PlannedState, Config: config,
		})
		require.NoError(t, err)

		return response
	}
	create := apply(nullOf(t, typ), config)
	require.Empty(t, create.Diagnostics)

	created := attributes(t, typ, create.NewState)
	require.True(t, created["value_wo"].IsNull())

	var revision string
	require.NoError(t, created["revision"].As(&revision))
	require.NotEmpty(t, revision)
	require.NotEqual(t, "private-value", revision)

	read, err := server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: name, CurrentState: create.NewState})
	require.NoError(t, err)
	require.Empty(t, read.Diagnostics)
	require.Equal(t, created, attributes(t, typ, read.NewState))
	require.Equal(t, created, attributes(t, typ, plan(read.NewState, config).PlannedState))

	_, rotatedConfig := object(t, schema, map[string]any{"name": "password", "version": "2", "value_wo": "rotated-private-value"})
	rotated := apply(read.NewState, rotatedConfig)
	require.Empty(t, rotated.Diagnostics)

	installed := attributes(t, typ, rotated.NewState)
	require.NotEqual(t, created["revision"], installed["revision"])
	require.True(t, installed["value_wo"].IsNull())

	// A failed update cannot publish a successful installation revision.
	client.fail = true
	_, failedConfig := object(t, schema, map[string]any{"name": "password", "version": "3", "value_wo": "failed-private-value"})
	failed := apply(rotated.NewState, failedConfig)
	require.NotEmpty(t, failed.Diagnostics)
	require.Equal(t, installed, attributes(t, typ, failed.NewState))

	client.fail = false

	// External deletion must cause recreation even without a version bump.
	client.present = false
	missing, err := server.ReadResource(t.Context(), &tfprotov6.ReadResourceRequest{TypeName: name, CurrentState: rotated.NewState})
	require.NoError(t, err)
	require.Empty(t, missing.Diagnostics)

	absent, err := missing.NewState.Unmarshal(typ)
	require.NoError(t, err)
	require.True(t, absent.IsNull())

	recreated := apply(missing.NewState, rotatedConfig)
	require.Empty(t, recreated.Diagnostics)

	replacement := attributes(t, typ, recreated.NewState)
	require.Equal(t, installed["version"], replacement["version"])
	require.NotEqual(t, installed["revision"], replacement["revision"])
	require.Equal(t, replacement, attributes(t, typ, plan(recreated.NewState, rotatedConfig).PlannedState))

	// The actual computed output drives the consumer, not its input version.
	consumer, deploymentSchema, _, units := lifecycleServer(t)

	var before, after string
	require.NoError(t, installed["revision"].As(&before))
	require.NoError(t, replacement["revision"].As(&after))

	consumerConfig := func(revision string) (tftypes.Object, *tfprotov6.DynamicValue) {
		return object(t, deploymentSchema, map[string]any{
			"name": "app", "files": stringMap("systemd/user/app.service", "[Service]\nExecStart=/bin/true\n"),
			"restart": []tftypes.Value{tftypes.NewValue(tftypes.String, "app.service")}, "triggers": stringMap("password", revision),
		})
	}
	consumerType, original := consumerConfig(before)
	initial := planConfig(t, consumer, nullOf(t, consumerType), original, original)
	deployed, err := consumer.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "quadlet_deployment", PriorState: nullOf(t, consumerType), PlannedState: initial.PlannedState, Config: original,
	})
	require.NoError(t, err)
	require.Empty(t, deployed.Diagnostics)

	units.restarted = nil
	_, changed := consumerConfig(after)
	update := planConfig(t, consumer, deployed.NewState, proposed(t, deploymentSchema, deployed.NewState, changed), changed)
	activated, err := consumer.ApplyResourceChange(t.Context(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "quadlet_deployment", PriorState: deployed.NewState, PlannedState: update.PlannedState, Config: changed,
	})
	require.NoError(t, err)
	require.Empty(t, activated.Diagnostics)
	require.Equal(t, []string{"app.service"}, units.restarted)

	noop := planConfig(t, consumer, activated.NewState, proposed(t, deploymentSchema, activated.NewState, changed), changed)
	require.Equal(t, attributes(t, consumerType, activated.NewState), attributes(t, consumerType, noop.PlannedState))
}
