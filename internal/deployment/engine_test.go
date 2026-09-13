// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/savely-krasovsky/terraform-provider-homelab-helpers/internal/remote"
)

type fakeHost struct {
	root          string
	calls         []remote.Command
	secrets       map[string]string
	secretWrites  int
	failRestart   bool
	failNft       bool
	failNftApply  bool
	failGenerator bool
	volumes       []string
	reads         map[string]int
	uploads       []string
	installed     []string
	mkdirs        []string
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
	if strings.Contains(name, "podman/") || strings.Contains(name, "podman-system-generator") {
		return os.Stat("/bin/sh")
	}

	return os.Lstat(name)
}

func (h *fakeHost) ReadFile(name string) ([]byte, error) {
	h.reads[name]++
	if err := h.CheckPath(name); err != nil {
		return nil, err
	}

	return os.ReadFile(name)
}

func (h *fakeHost) Mkdir(name string, mode fs.FileMode) error {
	h.mkdirs = append(h.mkdirs, name)

	return os.MkdirAll(name, mode)
}

func (h *fakeHost) WriteFile(ctx context.Context, name string, data []byte, mode fs.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	return h.writeFile(name, data, mode)
}

func (h *fakeHost) Upload(name string, data []byte, mode fs.FileMode) error {
	h.uploads = append(h.uploads, name)

	return h.writeFile(name, data, mode)
}

func (h *fakeHost) writeFile(name string, data []byte, mode fs.FileMode) error {
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

func (h *fakeHost) Run(ctx context.Context, command remote.Command) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h.calls = append(h.calls, remote.Command{Name: command.Name, Args: slices.Clone(command.Args)})
	name, args := command.Name, slices.Clone(command.Args)
	if name == "sudo" {
		name, args = args[1], args[2:]
	}
	for i, arg := range args {
		if suffix, ok := strings.CutPrefix(arg, "/etc/credstore"); ok {
			args[i] = path.Join(h.root, "credentials") + suffix
		}
	}

	switch name {
	case "/usr/libexec/podman/quadlet":
		if h.failGenerator {
			return nil, errors.New("invalid Quadlet")
		}
		for _, unit := range []string{"app.service", "other.service", "pod-pod.service"} {
			generated := "[Service]\nExecStart=/usr/bin/podman run --name app"
			if unit == "app.service" {
				for _, volume := range h.volumes {
					generated += " -v " + volume
				}
			}
			generated += " image\nExecStopPost=/usr/bin/podman rm -v -f -i app\n"

			if err := os.WriteFile(path.Join(args[len(args)-1], unit), []byte(generated), 0644); err != nil {
				return nil, err
			}
		}

	case "nft":
		if h.failNft || (h.failNftApply && !slices.Contains(args, "--check")) {
			return nil, errors.New("invalid firewall")
		}

	case "systemctl":
		if slices.Contains(args, "restart") && h.failRestart {
			return nil, errors.New("restart failed")
		}
		if slices.Contains(args, "show") {
			return []byte("loaded\n"), nil
		}
		if slices.Contains(args, "is-enabled") {
			return []byte("enabled\n"), nil
		}

	case "podman":
		if args[1] == "inspect" {
			if _, exists := h.secrets[args[2]]; !exists {
				return nil, os.ErrNotExist
			}
			return nil, nil
		}
		value, err := io.ReadAll(command.Stdin)
		if err != nil {
			return nil, err
		}
		h.secrets[args[3]] = string(value)
		h.secretWrites++

	case "id":
		return []byte("1000\n"), nil
	case "sync", "restorecon":
		return nil, nil
	case "test":
		info, err := os.Stat(args[len(args)-1])
		if slices.Contains(args, "!") {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, nil
			}
			return nil, errors.New("exists")
		}
		if err != nil {
			return nil, err
		}
		if args[0] == "-s" && info.Size() == 0 {
			return nil, errors.New("empty")
		}

	case "sha256sum":
		data, err := os.ReadFile(args[len(args)-1])
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		return []byte(hex.EncodeToString(sum[:]) + "  file\n"), nil

	case "mktemp":
		file, err := os.CreateTemp(path.Dir(args[0]), ".homelab-install.")
		if err != nil {
			return nil, err
		}
		defer file.Close()
		return []byte(strings.Replace(file.Name(), path.Join(h.root, "credentials"), "/etc/credstore", 1) + "\n"), nil

	case "install":
		if slices.Contains(args, "-d") {
			return nil, os.MkdirAll(args[len(args)-1], 0700)
		}
		data, err := os.ReadFile(args[len(args)-2])
		if err != nil {
			return nil, err
		}
		file := args[len(args)-1]
		if err := os.WriteFile(file, data, 0644); err != nil {
			return nil, err
		}
		return nil, os.Chmod(file, 0644)

	case "tee":
		data, err := io.ReadAll(command.Stdin)
		if err != nil {
			return nil, err
		}
		return nil, os.WriteFile(args[0], data, 0600)

	case "mv":
		return nil, os.Rename(args[len(args)-2], args[len(args)-1])
	case "rm":
		return nil, h.Remove(args[len(args)-1])
	default:
		return nil, fmt.Errorf("unexpected command: %s", name)
	}

	return nil, nil
}

func fixtureEngine(t *testing.T) (Engine, *fakeHost, map[string]string, Payload) {
	t.Helper()
	root := t.TempDir()
	host := &fakeHost{root: root, secrets: map[string]string{}, reads: map[string]int{}}
	values := map[string]string{"app_password": "secret\n"}
	engine := Engine{Host: host, Paths: Paths{Home: path.Join(root, "home"), Firewall: path.Join(root, "nft/main.nft")}}
	require.NoError(t, host.Mkdir(engine.Paths.State(), 0700))
	require.NoError(t, host.Mkdir(engine.Paths.Config(), 0755))

	payload := Payload{
		Files:           map[string]string{"containers/systemd/app.container": "[Container]\nImage=app\n"},
		Units:           []string{"app.service"},
		Groups:          map[string]Group{"app": {Units: []string{"app.service"}, Enable: []string{}, Hash: "first", UsesSecrets: true}},
		Firewall:        "table inet filter {}\n",
		Secrets:         map[string]string{"app_password": "id"},
		SecretsRevision: "first",
	}

	return engine, host, values, payload
}

func (h *fakeHost) restarts() []string {
	var result []string
	for _, call := range h.calls {
		if call.Name == "systemctl" && len(call.Args) > 1 && call.Args[1] == "restart" {
			result = append(result, call.Args[2:]...)
		}
	}

	return unique(result)
}

func TestApplyDriftAndRecovery(t *testing.T) {
	engine, host, values, payload := fixtureEngine(t)
	require.NoError(t, engine.Apply(t.Context(), payload, values))
	require.Equal(t, []string{"app.service"}, host.restarts())
	require.Equal(t, "secret\n", host.secrets["app-password"])
	require.Equal(t, 1, host.secretWrites)

	revision, err := engine.Read(t.Context(), payload)
	require.NoError(t, err)
	require.Equal(t, Digest(payload), revision)

	host.calls = nil
	require.NoError(t, engine.Apply(t.Context(), payload, values))
	require.Empty(t, host.restarts())
	require.Equal(t, 1, host.secretWrites)

	// A missing Podman secret must schedule an update and re-import on apply.
	delete(host.secrets, "app-password")
	revision, err = engine.Read(t.Context(), payload)
	require.NoError(t, err)
	require.NotEqual(t, Digest(payload), revision)
	require.NoError(t, engine.Apply(t.Context(), payload, values))
	require.Equal(t, 2, host.secretWrites)

	// Files may have landed before a restart failed. Retry must still restart.
	payload.Files["containers/systemd/app.container"] += "Environment=UPDATED=yes\n"
	group := payload.Groups["app"]
	group.Hash = "second"
	payload.Groups["app"] = group
	host.failRestart = true
	require.ErrorContains(t, engine.Apply(t.Context(), payload, values), "journal retained")
	require.FileExists(t, path.Join(engine.Paths.State(), "config-pending.json"))

	var previous manifest
	_, err = readJSON(host, path.Join(engine.Paths.State(), "config-manifest.json"), &previous)
	require.NoError(t, err)
	require.Equal(t, "first", previous.Groups["app"].Hash)

	host.failRestart = false
	host.calls = nil
	require.NoError(t, engine.Apply(t.Context(), payload, values))
	require.Equal(t, []string{"app.service"}, host.restarts())
	require.NoFileExists(t, path.Join(engine.Paths.State(), "config-pending.json"))
}

func TestValidationBeforeSecretsOrLiveChanges(t *testing.T) {
	for _, failure := range []string{"quadlet", "firewall"} {
		t.Run(failure, func(t *testing.T) {
			engine, host, values, payload := fixtureEngine(t)
			switch failure {
			case "quadlet":
				host.failGenerator = true
			case "firewall":
				host.failNft = true
			}

			require.Error(t, engine.Apply(t.Context(), payload, values))
			require.Empty(t, host.secrets)
			require.Empty(t, host.restarts())
			require.NoFileExists(t, path.Join(engine.Paths.Config(), "containers/systemd/app.container"))
			require.Zero(t, host.secretWrites)
		})
	}
}

func TestApplyReadsAndUploadsEachFileOnce(t *testing.T) {
	engine, host, values, payload := fixtureEngine(t)
	payload.Files["app/settings.conf"] = "unchanged"
	require.NoError(t, engine.Apply(t.Context(), payload, values))

	payload.Files["containers/systemd/app.container"] += "Environment=UPDATED=yes\n"
	group := payload.Groups["app"]
	group.Hash = "updated"
	payload.Groups["app"] = group

	clear(host.reads)
	host.uploads = nil
	host.installed = nil
	require.NoError(t, engine.Apply(t.Context(), payload, values))

	for name := range payload.Files {
		require.Equal(t, 1, host.reads[path.Join(engine.Paths.Config(), name)], name)
	}
	require.Len(t, host.uploads, len(payload.Files)+1) // Configuration and firewall staging.
	require.Equal(t, []string{path.Join(engine.Paths.Config(), "containers/systemd/app.container")}, host.installed)
}

func TestMissingEmptyFileIsRestored(t *testing.T) {
	engine, host, values, payload := fixtureEngine(t)
	payload.Files["app/empty.conf"] = ""
	require.NoError(t, engine.Apply(t.Context(), payload, values))

	filename := path.Join(engine.Paths.Config(), "app/empty.conf")
	require.NoError(t, os.Remove(filename))
	revision, err := engine.Read(t.Context(), payload)
	require.NoError(t, err)
	require.NotEqual(t, Digest(payload), revision)

	host.calls = nil
	require.NoError(t, engine.Apply(t.Context(), payload, values))
	require.FileExists(t, filename)
	require.Equal(t, []string{"app.service"}, host.restarts())
}

func TestFirewallApplyFailurePreservesConfiguration(t *testing.T) {
	engine, host, values, payload := fixtureEngine(t)
	require.NoError(t, engine.Apply(t.Context(), payload, values))

	previous := payload.Firewall
	payload.Firewall = "table inet updated {}\n"
	host.failNftApply = true
	require.Error(t, engine.Apply(t.Context(), payload, values))
	require.FileExists(t, path.Join(engine.Paths.State(), "config-pending.json"))

	content, err := os.ReadFile(engine.Paths.Firewall)
	require.NoError(t, err)
	require.Equal(t, previous, string(content))

	host.failNftApply = false
	require.NoError(t, engine.Apply(t.Context(), payload, values))
	content, err = os.ReadFile(engine.Paths.Firewall)
	require.NoError(t, err)
	require.Equal(t, payload.Firewall, string(content))
	require.NoFileExists(t, path.Join(engine.Paths.State(), "config-pending.json"))
}

func TestDataDirectoriesFollowGeneratedUnits(t *testing.T) {
	engine, host, values, payload := fixtureEngine(t)

	// An unused data root must stay out of the fingerprint, so that deployments
	// predating the feature do not report drift.
	require.NotContains(t, string(mustJSON(t, payload)), "data_")

	payload.DataRoot = path.Join(host.root, "data")
	existing := path.Join(payload.DataRoot, "app/postgres")
	require.NoError(t, os.MkdirAll(existing, 0700))

	outside := path.Join(host.root, "media")
	host.volumes = []string{
		existing + ":/var/lib/postgresql:Z",
		path.Join(payload.DataRoot, "app/valkey") + ":/data:Z",
		path.Join(payload.DataRoot, "fresh/deep/data") + ":/data",
		outside + ":/media:ro",
		"named-volume:/assets",
	}

	host.mkdirs = nil
	require.NoError(t, engine.Apply(t.Context(), payload, values))
	require.Equal(t, []string{
		path.Join(payload.DataRoot, "app/valkey"),
		path.Join(payload.DataRoot, "fresh"),
		path.Join(payload.DataRoot, "fresh/deep"),
		path.Join(payload.DataRoot, "fresh/deep/data"),
	}, created(host, payload.DataRoot))

	// A mount below the root is data; anything else belongs to whatever provides
	// it, and creating it would mask a share that failed to mount.
	require.NoDirExists(t, outside)

	// An existing directory keeps the mode the application gave it.
	info, err := os.Stat(existing)
	require.NoError(t, err)
	require.Equal(t, fs.FileMode(0700), info.Mode().Perm())

	// Dropping a volume must never remove the data behind it.
	dropped := path.Join(payload.DataRoot, "app/valkey")
	host.volumes = host.volumes[:1]
	host.mkdirs = nil
	require.NoError(t, engine.Apply(t.Context(), payload, values))
	require.Empty(t, created(host, payload.DataRoot))
	require.DirExists(t, dropped)
}

func TestDataRootIsNeverCreated(t *testing.T) {
	engine, host, values, payload := fixtureEngine(t)
	payload.DataRoot = path.Join(host.root, "unmounted")
	host.volumes = []string{path.Join(payload.DataRoot, "app/valkey") + ":/data:Z"}

	// A data root is a mount point in practice: creating it would hide a
	// filesystem that failed to mount behind fresh empty directories.
	require.ErrorIs(t, engine.Apply(t.Context(), payload, values), fs.ErrNotExist)
	require.NoDirExists(t, payload.DataRoot)
	require.Empty(t, host.restarts())
	require.FileExists(t, path.Join(engine.Paths.State(), "config-pending.json"))
}

func TestMountSourcesFromPodmanArguments(t *testing.T) {
	unit := strings.Join([]string{
		"ExecStart=/usr/bin/podman run --name app",
		"-v /srv/data/app:/data:Z",
		"--volume=/srv/data/other:/other",
		`--volume "/srv/data/quoted:/quoted"`,
		"--mount type=bind,source=/srv/data/mounted,destination=/mounted",
		"--mount type=bind,src=/srv/data/short,dst=/short",
		"-v named-volume:/assets",
		"-v %E/app/settings.conf:/etc/app.conf",
		"--label source=inline",
		"\nExecStopPost=/usr/bin/podman rm -v -f -i app",
	}, " ")

	// Named volumes, unexpanded specifiers and the -v of podman rm are not
	// absolute paths, so they never reach the filesystem.
	require.Equal(t, []string{
		"/srv/data/app",
		"/srv/data/other",
		"/srv/data/quoted",
		"/srv/data/mounted",
		"/srv/data/short",
	}, mountSources(unit))
}

func TestDataRootValidation(t *testing.T) {
	for name, root := range map[string]string{
		"relative": "srv/data",
		"unclean":  "/srv/data/",
		"root":     "/",
		"newline":  "/srv/data\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, payload := fixtureEngine(t)
			payload.DataRoot = root
			require.Error(t, payload.Validate())
		})
	}

	_, _, _, payload := fixtureEngine(t)
	payload.DataRoot = "/srv/data"
	require.NoError(t, payload.Validate())
}

func created(host *fakeHost, root string) []string {
	return slices.DeleteFunc(slices.Clone(host.mkdirs), func(dir string) bool {
		return !strings.HasPrefix(dir, root+"/")
	})
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)

	return data
}

func TestDeletePreservesUnmanagedData(t *testing.T) {
	engine, host, values, payload := fixtureEngine(t)
	require.NoError(t, engine.Apply(t.Context(), payload, values))
	unmanaged := path.Join(engine.Paths.Config(), "personal.conf")
	require.NoError(t, os.WriteFile(unmanaged, []byte("keep"), 0644))

	require.NoError(t, engine.Delete(t.Context(), Ownership{
		Files: slices.Collect(maps.Keys(payload.Files)),
		Units: payload.Units,
	}))
	require.NoFileExists(t, path.Join(engine.Paths.Config(), "containers/systemd/app.container"))
	require.FileExists(t, unmanaged)
	require.FileExists(t, engine.Paths.Firewall)
	require.Contains(t, host.secrets, "app-password")
	require.NoFileExists(t, path.Join(engine.Paths.State(), "config-manifest.json"))
}

func TestLegacyManifestAndPendingOwnership(t *testing.T) {
	engine, host, values, payload := fixtureEngine(t)
	oldFile := "old.conf"
	pendingFile := "interrupted.conf"
	for _, name := range []string{oldFile, pendingFile} {
		require.NoError(t, os.WriteFile(path.Join(engine.Paths.Config(), name), []byte("old"), 0644))
	}

	require.NoError(t, writeJSON(t.Context(), host, path.Join(engine.Paths.State(), "config-manifest.json"), Ownership{Files: []string{oldFile}, Units: []string{"old.service"}}))
	require.NoError(t, writeJSON(t.Context(), host, path.Join(engine.Paths.State(), "config-pending.json"), Ownership{Files: []string{pendingFile}}))
	require.NoError(t, engine.Apply(t.Context(), payload, values))
	require.NoFileExists(t, path.Join(engine.Paths.Config(), oldFile))
	require.NoFileExists(t, path.Join(engine.Paths.Config(), pendingFile))
}

func TestSymlinkAndCorruptJournal(t *testing.T) {
	engine, host, values, payload := fixtureEngine(t)
	require.NoError(t, os.Symlink(t.TempDir(), path.Join(engine.Paths.Config(), "containers")))
	require.ErrorContains(t, engine.Apply(t.Context(), payload, values), "symlink")

	require.NoError(t, os.Remove(path.Join(engine.Paths.Config(), "containers")))
	require.NoError(t, host.WriteFile(t.Context(), path.Join(engine.Paths.State(), "config-pending.json"), []byte(`{"files":["../../escape"]}`), 0600))
	require.ErrorContains(t, engine.Apply(t.Context(), payload, values), "invalid managed path")
}

func TestResticCredentialBytesAndMode(t *testing.T) {
	engine, host, _, payload := fixtureEngine(t)
	payload.Secrets = map[string]string{"restic_password": "id"}
	values := map[string]string{"restic_password": "password\n\n"}
	require.NoError(t, engine.Apply(t.Context(), payload, values))

	file := filepath.Join(host.root, "credentials/restic-password")
	value, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "password\n\n", string(value))
	info, err := os.Stat(file)
	require.NoError(t, err)
	require.EqualValues(t, 0600, info.Mode().Perm())

	manifest, err := os.ReadFile(path.Join(engine.Paths.State(), "config-manifest.json"))
	require.NoError(t, err)
	require.NotContains(t, string(manifest), "password\\n")
	calls, err := json.Marshal(host.calls)
	require.NoError(t, err)
	require.NotContains(t, string(calls), "password\\n")
}

func TestChangedGroupAndSharedPod(t *testing.T) {
	engine, host, values, payload := fixtureEngine(t)
	payload.Files["containers/systemd/other.container"] = "other"
	payload.Units = append(payload.Units, "other.service", "pod-pod.service")
	payload.Groups["other"] = Group{Units: []string{"other.service", "pod-pod.service"}, Enable: []string{}, Hash: "old"}
	require.NoError(t, engine.Apply(t.Context(), payload, values))

	host.calls = nil
	payload.Files["containers/systemd/other.container"] = "changed"
	group := payload.Groups["other"]
	group.Hash = "new"
	payload.Groups["other"] = group
	require.NoError(t, engine.Apply(t.Context(), payload, values))
	require.Equal(t, []string{"other.service", "pod-pod.service"}, host.restarts())
}

func TestDriftInUnchangedGroupDuringAnotherGroupUpdate(t *testing.T) {
	engine, host, values, payload := fixtureEngine(t)
	payload.Files["containers/systemd/other.container"] = "other"
	payload.Units = append(payload.Units, "other.service")
	payload.Groups["other"] = Group{Units: []string{"other.service"}, Enable: []string{}, Hash: "old"}
	require.NoError(t, engine.Apply(t.Context(), payload, values))

	require.NoError(t, os.WriteFile(path.Join(engine.Paths.Config(), "containers/systemd/app.container"), []byte("manual drift"), 0644))
	payload.Files["containers/systemd/other.container"] = "new"
	group := payload.Groups["other"]
	group.Hash = "new"
	payload.Groups["other"] = group
	host.calls = nil

	require.NoError(t, engine.Apply(t.Context(), payload, values))
	require.Equal(t, []string{"app.service", "other.service"}, host.restarts())
}

func TestSecretRotationRequiresRevision(t *testing.T) {
	engine, host, values, payload := fixtureEngine(t)
	require.NoError(t, engine.Apply(t.Context(), payload, values))

	original := Digest(payload)
	values["app_password"] = "rotated\nsecret\n"
	host.calls = nil
	require.NoError(t, engine.Apply(t.Context(), payload, values))
	require.Equal(t, original, Digest(payload))
	require.Equal(t, "secret\n", host.secrets["app-password"])
	require.Empty(t, host.restarts())

	payload.SecretsRevision = "rotated"
	require.NoError(t, engine.Apply(t.Context(), payload, values))
	require.Equal(t, values["app_password"], host.secrets["app-password"])
	require.Equal(t, []string{"app.service"}, host.restarts())

	manifest, err := os.ReadFile(path.Join(engine.Paths.State(), "config-manifest.json"))
	require.NoError(t, err)
	require.NotContains(t, string(manifest), "rotated\\nsecret")
}
