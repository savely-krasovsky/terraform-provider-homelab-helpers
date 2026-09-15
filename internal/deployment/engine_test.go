// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	hostio "github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/host"
	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/local"
	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/quadlet"
	"runtime"
)

// fakeHost keeps a real directory tree and emulates the few commands the engine runs.
type fakeHost struct {
	root           string
	generatorError error
	strayUnit      string
	installed      []string
	mkdirs         []string
	calls          []string
}

func (h *fakeHost) CheckPath(name string) error {
	current := "/"
	for part := range strings.SplitSeq(strings.TrimPrefix(name, "/"), "/") {
		current = path.Join(current, part)

		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		if err != nil {
			return err
		}

		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("symlink: %s", current)
		}
	}

	return nil
}

func (h *fakeHost) Stat(name string) (fs.FileInfo, error) {
	if strings.Contains(name, "podman/quadlet") {
		return os.Stat("/bin/sh")
	}

	return os.Lstat(name)
}

func (h *fakeHost) ReadFile(name string) ([]byte, error) {
	if err := h.CheckPath(name); err != nil {
		return nil, err
	}

	return os.ReadFile(name)
}

func (h *fakeHost) ReadDir(name string) ([]string, error) {
	entries, err := os.ReadDir(name)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names, nil
}

func (h *fakeHost) Mkdir(name string, mode fs.FileMode) error {
	h.mkdirs = append(h.mkdirs, name)

	return os.MkdirAll(name, mode)
}

func (h *fakeHost) WriteFile(ctx context.Context, name string, data []byte, mode fs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	return h.Upload(name, data, mode)
}

func (h *fakeHost) Upload(name string, data []byte, mode fs.FileMode) error {
	if err := h.CheckPath(name); err != nil {
		return err
	}

	if err := os.MkdirAll(path.Dir(name), 0755); err != nil {
		return err
	}

	return os.WriteFile(name, data, mode)
}

func (h *fakeHost) InstallFile(ctx context.Context, source, destination string) error {
	h.installed = append(h.installed, destination)

	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}

	return h.WriteFile(ctx, destination, data, 0644)
}

func (h *fakeHost) Remove(name string) error {
	err := os.Remove(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	return err
}

func (h *fakeHost) RemoveStage(name string) error { return os.RemoveAll(name) }

func (h *fakeHost) Run(ctx context.Context, command hostio.Command) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	name, args := command.Name, slices.Clone(command.Args)
	if name == "sudo" {
		name, args = args[1], args[2:]
	}

	h.calls = append(h.calls, name+" "+strings.Join(args, " "))
	for i, arg := range args {
		if suffix, ok := strings.CutPrefix(arg, "/etc/"); ok {
			args[i] = path.Join(h.root, "etc", suffix)
		}
	}

	switch name {
	case "/usr/libexec/podman/quadlet":
		if h.generatorError != nil {
			return nil, h.generatorError
		}

		// Name the units the way the real generator would, from the staged tree.
		staged := strings.TrimPrefix(command.Env[0], "QUADLET_UNIT_DIRS=")
		suffixes := map[string]string{".container": ".service", ".pod": "-pod.service", ".network": "-network.service", ".volume": "-volume.service"}

		err := filepath.WalkDir(staged, func(file string, entry fs.DirEntry, err error) error {
			suffix, quadlet := suffixes[path.Ext(file)]
			if err != nil || entry.IsDir() || !quadlet {
				return err
			}

			unit := strings.TrimSuffix(path.Base(file), path.Ext(file)) + suffix

			return os.WriteFile(path.Join(args[len(args)-1], unit), []byte("[Service]\nExecStart=/usr/bin/podman run\n"), 0644)
		})
		if err != nil {
			return nil, err
		}

		if h.strayUnit != "" {
			return nil, os.WriteFile(path.Join(args[len(args)-1], h.strayUnit), []byte("[Service]\n"), 0644)
		}

	case "sync":
	default:
		return nil, fmt.Errorf("unexpected command: %s", name)
	}

	return nil, nil
}

// fakeUnits records what the engine asks of the user manager.
type fakeUnits struct {
	missing     []string
	failRestart bool
	reloads     int
	enabled     []string
	disabled    []string
	stopped     []string
	restarted   []string
	conditional []string
}

func (u *fakeUnits) Reload(context.Context) error { u.reloads++; return nil }
func (u *fakeUnits) Enable(_ context.Context, units []string) error {
	u.enabled = append(u.enabled, units...)
	return nil
}
func (u *fakeUnits) Disable(_ context.Context, units []string) error {
	u.disabled = append(u.disabled, units...)
	return nil
}
func (u *fakeUnits) Loaded(_ context.Context, units []string) ([]string, error) {
	return slices.DeleteFunc(slices.Clone(units), func(unit string) bool { return slices.Contains(u.missing, unit) }), nil
}
func (u *fakeUnits) Stop(_ context.Context, units []string) error {
	u.stopped = append(u.stopped, units...)
	return nil
}
func (u *fakeUnits) Restart(_ context.Context, units []string) error {
	if u.failRestart {
		return errors.New("restart failed")
	}

	u.restarted = append(u.restarted, units...)

	return nil
}

func fixture(t *testing.T) (Engine, *fakeHost, *fakeUnits, Payload) {
	t.Helper()

	root := t.TempDir()
	host := &fakeHost{root: root}
	units := &fakeUnits{}
	engine := Engine{Host: host, Quadlets: quadlet.Validator{Host: host}, Units: units, Name: "apps", ID: "owner-apps", Paths: Paths{Config: path.Join(root, "home/.config"), State: path.Join(root, "home/.local/state/terraform-quadlet")}}
	require.NoError(t, Prepare(t.Context(), host, engine.Paths))

	payload := Payload{
		Files: map[string]string{
			"containers/systemd/app.container":   "[Container]\nImage=app\nVolume=%E/app/settings.conf:/etc/app.conf\nSecret=app-password\n",
			"containers/systemd/other.container": "[Container]\nImage=other\n",
			"app/settings.conf":                  "level=info\n",
		},
		Restart:  []string{"app.service", "other.service"},
		Triggers: map[string]string{"app-password": "1"},
	}

	return engine, host, units, payload
}

func TestApplyThenReadAndNoop(t *testing.T) {
	engine, host, units, payload := fixture(t)
	require.NoError(t, apply(t.Context(), engine, payload))
	require.ElementsMatch(t, []string{"app.service", "other.service"}, units.restarted)
	require.Len(t, host.installed, 3)

	snapshot, err := engine.Read(t.Context())
	require.NoError(t, err)
	require.True(t, snapshot.Found)
	require.Equal(t, payload.Files, snapshot.Files)
	require.Equal(t, Digest(payload), snapshot.Revision)

	units.restarted, host.installed = nil, nil

	require.NoError(t, apply(t.Context(), engine, payload))
	require.Empty(t, units.restarted)
	require.Empty(t, host.installed)
	require.FileExists(t, path.Join(engine.StateDir(), recordFile))
}

func TestReadWithoutDeployment(t *testing.T) {
	engine, _, _, _ := fixture(t)
	snapshot, err := engine.Read(t.Context())
	require.NoError(t, err)
	require.False(t, snapshot.Found)
}

func TestChangedAndDriftedFilesActivateTheDeployment(t *testing.T) {
	engine, host, units, payload := fixture(t)
	require.NoError(t, apply(t.Context(), engine, payload))

	units.restarted = nil
	payload.Files["app/settings.conf"] = "level=debug\n"
	require.NoError(t, apply(t.Context(), engine, payload))
	require.Equal(t, []string{"app.service", "other.service"}, units.restarted)

	units.restarted = nil

	require.NoError(t, os.WriteFile(path.Join(engine.Paths.Config, "containers/systemd/other.container"), []byte("manual drift"), 0644))
	require.NoError(t, apply(t.Context(), engine, payload))
	require.Equal(t, []string{"app.service", "other.service"}, units.restarted)
	require.Len(t, host.installed, 5)
}

func TestTriggerVersionActivatesTheDeployment(t *testing.T) {
	engine, _, units, payload := fixture(t)
	require.NoError(t, apply(t.Context(), engine, payload))

	units.restarted = nil
	payload.Triggers["app-password"] = "2"
	require.NoError(t, apply(t.Context(), engine, payload))
	require.Equal(t, []string{"app.service", "other.service"}, units.restarted)
}

func TestInterruptedApplyRestartsEverything(t *testing.T) {
	engine, _, units, payload := fixture(t)
	require.NoError(t, apply(t.Context(), engine, payload))

	payload.Files["app/settings.conf"] = "level=debug\n"
	units.failRestart = true

	require.ErrorContains(t, apply(t.Context(), engine, payload), "ownership retained")
	require.FileExists(t, path.Join(engine.StateDir(), recordFile))

	units.failRestart = false
	units.restarted = nil

	require.NoError(t, apply(t.Context(), engine, payload))
	require.ElementsMatch(t, []string{"app.service", "other.service"}, units.restarted)
	require.FileExists(t, path.Join(engine.StateDir(), recordFile))
}

func TestRetiredDefinitionsAreStoppedAndRemoved(t *testing.T) {
	engine, _, units, payload := fixture(t)
	payload.Files["systemd/user/backup.timer"] = "[Timer]\n[Install]\nWantedBy=timers.target\n"
	payload.Files["systemd/user/backup.service"] = "[Service]\n"
	payload.Restart = append(payload.Restart, "backup.timer")
	payload.Enable = []string{"backup.timer"}

	require.NoError(t, apply(t.Context(), engine, payload))
	require.Equal(t, []string{"backup.timer"}, units.enabled)

	delete(payload.Files, "containers/systemd/other.container")
	delete(payload.Files, "systemd/user/backup.timer")
	delete(payload.Files, "systemd/user/backup.service")

	payload.Restart = []string{"app.service"}
	payload.Enable = nil

	units.missing = []string{"backup.service"}

	require.NoError(t, apply(t.Context(), engine, payload))
	require.ElementsMatch(t, []string{"other.service", "backup.timer"}, units.stopped)
	require.Contains(t, units.disabled, "backup.service")
	require.Contains(t, units.disabled, "backup.timer")
	require.NoFileExists(t, path.Join(engine.Paths.Config, "containers/systemd/other.container"))
	require.NoFileExists(t, path.Join(engine.Paths.Config, "systemd/user/backup.timer"))
}

func TestGeneratorOutputBecomesOwnedWithoutActivation(t *testing.T) {
	engine, host, units, payload := fixture(t)
	host.strayUnit = "surprise.service"

	result, err := engine.Apply(t.Context(), payload)
	require.NoError(t, err)
	require.Equal(t, []string{"app.service", "other.service", "surprise.service"}, result.Units)
	require.NotContains(t, units.restarted, "surprise.service")
	require.NoError(t, engine.Delete(t.Context()))
	require.Contains(t, units.stopped, "surprise.service")
}

func TestValidationFailureTouchesNothing(t *testing.T) {
	for _, failure := range []error{errors.New("invalid Quadlet"), fs.ErrNotExist} {
		engine, host, units, payload := fixture(t)
		host.generatorError = failure
		payload.Restart = nil

		require.ErrorIs(t, apply(t.Context(), engine, payload), failure)
		require.Empty(t, units.restarted)
		require.Empty(t, host.installed)
		require.NoFileExists(t, path.Join(engine.StateDir(), recordFile))
	}
}

func TestDeletePreservesUnmanagedFiles(t *testing.T) {
	engine, host, units, payload := fixture(t)
	require.NoError(t, apply(t.Context(), engine, payload))

	unmanaged := path.Join(engine.Paths.Config, "personal.conf")
	require.NoError(t, os.WriteFile(unmanaged, []byte("keep"), 0644))

	require.NoError(t, engine.Delete(t.Context()))
	require.ElementsMatch(t, []string{"app.service", "other.service"}, units.stopped)
	require.NoFileExists(t, path.Join(engine.Paths.Config, "containers/systemd/app.container"))
	require.NoFileExists(t, path.Join(engine.Paths.Config, "containers/systemd/other.container"))
	require.FileExists(t, unmanaged)
	require.NoFileExists(t, path.Join(engine.StateDir(), recordFile))

	_ = host
}

func TestRecordRejectsEscapes(t *testing.T) {
	engine, host, _, payload := fixture(t)
	require.NoError(t, host.WriteFile(t.Context(), path.Join(engine.StateDir(), recordFile), []byte(`{"version":3,"id":"owner-apps","files":["../../escape"]}`), 0600))
	require.ErrorContains(t, apply(t.Context(), engine, payload), "invalid managed path")
}

// apply keeps tests that only inspect effects focused on the error result.
func apply(ctx context.Context, engine Engine, payload Payload) error {
	_, err := engine.Apply(ctx, payload)
	return err
}

func (u *fakeUnits) TryRestart(_ context.Context, units []string) error {
	if u.failRestart {
		return errors.New("restart failed")
	}

	u.conditional = append(u.conditional, units...)

	return nil
}

func (h *fakeHost) Sync(ctx context.Context, name string) error {
	_, err := h.Run(ctx, hostio.Command{Name: "sync", Args: []string{"-f", name}})
	return err
}

// Only systemd and the Quadlet generator are simulated. Files, durable writes,
// symlink checks and the host lease use the real local transport.
func TestEngineWithLocalTransport(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("local transport requires Linux")
	}

	engine, _, units, payload := fixture(t)
	client, err := local.Connect(t.Context())
	require.NoError(t, err)

	defer client.Close()

	ctx, release, err := client.Lock(t.Context(), path.Join(engine.Paths.State, "config.lock"))
	require.NoError(t, err)

	defer release()

	engine.Host = client
	require.NoError(t, apply(ctx, engine, payload))

	before, err := engine.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, payload.Files, before.Files)
	require.NoError(t, client.WriteFile(ctx, path.Join(engine.Paths.Config, "app/settings.conf"), []byte("drift"), 0644))

	units.failRestart = true

	require.Error(t, apply(ctx, engine, payload))

	pending, err := engine.Read(ctx)
	require.NoError(t, err)
	require.Empty(t, pending.Revision)
	require.Equal(t, payload.Files, pending.Files)

	units.failRestart = false

	require.NoError(t, apply(ctx, engine, payload))
	require.NoError(t, engine.Delete(ctx))
	require.NoFileExists(t, path.Join(engine.Paths.Config, "app/settings.conf"))
}

func examplePayload(t *testing.T, name, message string) Payload {
	t.Helper()

	read := func(file string) string {
		content, err := os.ReadFile(filepath.Join("../../examples/independent-stacks", file))
		require.NoError(t, err)

		rendered := strings.ReplaceAll(string(content), "${name}", name)
		require.NotContains(t, rendered, "${")

		return rendered
	}
	if name == "network" {
		return Payload{
			Files:   map[string]string{quadletDir + "example.network": read("shared.network")},
			Restart: []string{"example-network.service"},
		}
	}

	unit := "example-" + name

	return Payload{
		Files: map[string]string{
			quadletDir + unit + ".container":                   read("app.container.tftpl"),
			quadletDir + unit + ".container.d/10-restart.conf": read("restart.conf"),
			nativeDir + unit + ".target":                       read("app.target.tftpl"),
			unit + "/index.html":                               message,
		},
		Restart: []string{unit + ".target"},
		Enable:  []string{unit + ".target"},
	}
}

func TestIndependentStacksWithHostGenerator(t *testing.T) {
	available := false

	for _, candidate := range []string{"/usr/libexec/podman/quadlet", "/usr/lib/systemd/system-generators/podman-system-generator"} {
		if _, err := os.Stat(candidate); err == nil {
			available = true
			break
		}
	}

	if !available || runtime.GOOS != "linux" {
		if os.Getenv("REQUIRE_QUADLET_TESTS") == "1" {
			t.Fatal("Linux and the host Quadlet generator are required")
		}

		t.Skip("Linux and the host Quadlet generator are not available")
	}

	engine, _, units, _ := fixture(t)
	client, err := local.Connect(t.Context())
	require.NoError(t, err)

	defer client.Close()

	ctx, release, err := client.Lock(t.Context(), path.Join(engine.Paths.State, "config.lock"))
	require.NoError(t, err)

	defer release()

	engine.Host = client
	engine.Quadlets = quadlet.Validator{Host: client}
	network, alpha, beta := engine, engine, engine
	network.Name, network.ID = "example-network", "owner-network"
	alpha.Name, alpha.ID = "example-alpha", "owner-alpha"
	beta.Name, beta.ID = "example-beta", "owner-beta"
	networkPayload := examplePayload(t, "network", "")
	alphaPayload := examplePayload(t, "alpha", "Hello from alpha")
	betaPayload := examplePayload(t, "beta", "Hello from beta")
	result, err := network.Apply(ctx, networkPayload)
	require.NoError(t, err)
	require.Equal(t, []string{"example-network.service"}, result.Units)

	result, err = alpha.Apply(ctx, alphaPayload)
	require.NoError(t, err)
	require.Equal(t, []string{"example-alpha.service", "example-alpha.target"}, result.Units)
	require.NoError(t, apply(ctx, beta, betaPayload))
	require.Equal(t, []string{"example-network.service", "example-alpha.target", "example-beta.target"}, units.restarted)

	stage := path.Join(t.TempDir(), "validation")
	for name, content := range alphaPayload.Files {
		require.NoError(t, client.Upload(path.Join(stage, "files", name), []byte(content), 0644))
	}

	discovered, err := engine.Quadlets.Discover(ctx, stage)
	require.NoError(t, err)
	require.Equal(t, []string{"example-alpha.service", "example-alpha.target"}, discovered)

	generated, err := client.ReadFile(path.Join(stage, "generated/example-alpha.service"))
	require.NoError(t, err)

	for _, dependency := range []string{"Requires=example-network.service", "After=example-network.service", "PartOf=example-alpha.target", "Restart=on-failure", "example"} {
		require.Contains(t, string(generated), dependency)
	}

	units.restarted = nil

	require.NoError(t, apply(ctx, network, networkPayload))
	require.NoError(t, apply(ctx, alpha, alphaPayload))
	require.NoError(t, apply(ctx, beta, betaPayload))
	require.Empty(t, units.restarted)

	alphaPayload.Files["example-alpha/index.html"] = "Updated alpha"
	require.NoError(t, apply(ctx, alpha, alphaPayload))
	require.Equal(t, []string{"example-alpha.target"}, units.restarted)

	alphaPayload.Files["example-alpha/index.html"] = "Retry alpha"
	units.failRestart = true

	require.ErrorContains(t, apply(ctx, alpha, alphaPayload), "restart failed")

	pending, err := alpha.Read(ctx)
	require.NoError(t, err)
	require.Empty(t, pending.Revision)

	units.failRestart, units.restarted = false, nil

	require.NoError(t, apply(ctx, alpha, alphaPayload))
	require.NoError(t, apply(ctx, beta, betaPayload))
	require.Equal(t, []string{"example-alpha.target"}, units.restarted)

	require.NoError(t, alpha.Delete(ctx))
	require.Equal(t, []string{"example-alpha.service", "example-alpha.target"}, units.stopped)

	for _, remaining := range []struct {
		engine  Engine
		payload Payload
	}{{network, networkPayload}, {beta, betaPayload}} {
		snapshot, err := remaining.engine.Read(ctx)
		require.NoError(t, err)
		require.True(t, snapshot.Found)
		require.Equal(t, remaining.payload.Files, snapshot.Files)
		require.Equal(t, Digest(remaining.payload), snapshot.Revision)
	}
}
