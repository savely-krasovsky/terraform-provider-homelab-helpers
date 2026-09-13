// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func tree() map[string]string {
	return map[string]string{
		"containers/systemd/reverse-proxy.network":    "[Network]\n",
		"containers/systemd/app-data.volume":          "[Volume]\n",
		"containers/systemd/podman.conf":              "[Engine]\n",
		"containers/systemd/traefik.container":        "[Container]\nVolume=%E/traefik/traefik.yml:/etc/traefik/traefik.yml:z\nSecret=cf_token,type=env\n",
		"traefik/traefik.yml":                         "entryPoints: {}\n",
		"containers/systemd/matrix.pod":               "[Pod]\n",
		"containers/systemd/matrix-synapse.container": "[Container]\nPod=matrix.pod\nVolume=%E/matrix:/data:z\nVolume=/var/mnt/docker/app_data/synapse:/store:z\nVolume=app-data.volume:/cache\n",
		"containers/systemd/matrix-auth.container":    "[Container]\nPod=matrix.pod\n",
		"matrix/homeserver.yaml":                      "server_name: example\n",
		"matrix/log.config":                           "version: 1\n",
		"systemd/user/restic-backup.timer":            "[Timer]\nOnCalendar=daily\n",
		"systemd/user/restic-backup.service":          "[Service]\nExecStart=/usr/bin/restic\n",
		"systemd/user/node-exporter.service":          "[Service]\nExecStart=/usr/bin/node_exporter\n",
	}
}

// The hash recipe has to stay compatible with sha256(jsonencode(...)) in HCL,
// which is how existing hosts recorded their restart groups. HCL escapes <, >
// and & the same way encoding/json does; this pins that agreement.
func TestDigestMatchesHCLJSONEncode(t *testing.T) {
	require.Equal(t,
		"a260a0e930e046453f809f3a6f88e3358be8759cf4f0c0d6c9a5591901304573",
		Digest(map[string]string{"b/x": "a<b>&c", "a": "eq"}),
	)
}

func TestDeriveGroupsByPod(t *testing.T) {
	units, groups, err := Derive(tree())
	require.NoError(t, err)

	require.ElementsMatch(t, []string{"traefik.service", "matrix-pod.service", "restic-backup.timer"}, keysOf(groups))

	require.Equal(t, []string{"traefik.service"}, groups["traefik.service"].Units)
	require.Equal(t,
		[]string{"matrix-pod.service", "matrix-auth.service", "matrix-synapse.service"},
		groups["matrix-pod.service"].Units,
	)
	require.Empty(t, groups["matrix-pod.service"].Enable)

	require.Subset(t, units, []string{
		"traefik.service", "matrix-pod.service", "matrix-synapse.service", "matrix-auth.service",
		"reverse-proxy-network.service", "app-data-volume.service",
		"node-exporter.service", "restic-backup.timer", "restic-backup.service",
	})
}

func TestDeriveHashCoversMountedConfiguration(t *testing.T) {
	files := tree()
	baseline, _, err := Derive(files)
	require.NoError(t, err)
	require.NotEmpty(t, baseline)

	// A directory mount pulls in the whole subtree below it.
	for _, name := range []string{"matrix/homeserver.yaml", "matrix/log.config"} {
		changed := tree()
		changed[name] += "# edited\n"
		_, groups, err := Derive(changed)
		require.NoError(t, err)

		_, original, err := Derive(tree())
		require.NoError(t, err)
		require.NotEqual(t, original["matrix-pod.service"].Hash, groups["matrix-pod.service"].Hash, name)
		require.Equal(t, original["traefik.service"].Hash, groups["traefik.service"].Hash, name)
	}

	// A single-file mount tracks only that file.
	changed := tree()
	changed["traefik/traefik.yml"] += "# edited\n"
	_, groups, err := Derive(changed)
	require.NoError(t, err)

	_, original, err := Derive(tree())
	require.NoError(t, err)
	require.NotEqual(t, original["traefik.service"].Hash, groups["traefik.service"].Hash)
	require.Equal(t, original["matrix-pod.service"].Hash, groups["matrix-pod.service"].Hash)
}

func TestDeriveHashCoversSharedQuadlets(t *testing.T) {
	_, original, err := Derive(tree())
	require.NoError(t, err)

	for _, name := range []string{
		"containers/systemd/reverse-proxy.network",
		"containers/systemd/app-data.volume",
		"containers/systemd/podman.conf",
	} {
		changed := tree()
		changed[name] += "# edited\n"
		_, groups, err := Derive(changed)
		require.NoError(t, err)

		for group := range original {
			if group == "restic-backup.timer" {
				require.Equal(t, original[group].Hash, groups[group].Hash, name)

				continue
			}
			require.NotEqual(t, original[group].Hash, groups[group].Hash, name)
		}
	}
}

func TestDeriveIgnoresDataAndNamedVolumes(t *testing.T) {
	// Only %E paths are configuration; an unrelated file must not move a hash.
	changed := tree()
	changed["glance/glance.yml"] = "pages: []\n"
	_, groups, err := Derive(changed)
	require.NoError(t, err)

	_, original, err := Derive(tree())
	require.NoError(t, err)
	for group := range original {
		require.Equal(t, original[group].Hash, groups[group].Hash, group)
	}
}

func TestDeriveSecrets(t *testing.T) {
	_, groups, err := Derive(tree())
	require.NoError(t, err)

	require.True(t, groups["traefik.service"].UsesSecrets)
	require.False(t, groups["matrix-pod.service"].UsesSecrets)
	require.False(t, groups["restic-backup.timer"].UsesSecrets)
}

func TestDeriveTimer(t *testing.T) {
	files := tree()
	_, groups, err := Derive(files)
	require.NoError(t, err)

	timer := groups["restic-backup.timer"]
	require.Equal(t, []string{"restic-backup.timer"}, timer.Units)
	require.Equal(t, []string{"restic-backup.timer"}, timer.Enable)
	require.Equal(t, Digest(map[string]string{
		"timer":   files["systemd/user/restic-backup.timer"],
		"service": files["systemd/user/restic-backup.service"],
	}), timer.Hash)

	changed := tree()
	changed["systemd/user/restic-backup.service"] += "# edited\n"
	_, updated, err := Derive(changed)
	require.NoError(t, err)
	require.NotEqual(t, timer.Hash, updated["restic-backup.timer"].Hash)
}

func TestDeriveRejectsIncompleteTree(t *testing.T) {
	orphan := tree()
	delete(orphan, "containers/systemd/matrix.pod")
	_, _, err := Derive(orphan)
	require.ErrorContains(t, err, "undeclared pod")

	timer := tree()
	delete(timer, "systemd/user/restic-backup.service")
	_, _, err = Derive(timer)
	require.ErrorContains(t, err, "timer has no service")
}

func TestDeriveIsDeterministic(t *testing.T) {
	units, groups, err := Derive(tree())
	require.NoError(t, err)

	for range 10 {
		repeatUnits, repeatGroups, err := Derive(tree())
		require.NoError(t, err)
		require.Equal(t, units, repeatUnits)
		require.Equal(t, groups, repeatGroups)
	}
}

func TestDeriveProducesValidPayload(t *testing.T) {
	units, groups, err := Derive(tree())
	require.NoError(t, err)

	require.NoError(t, Payload{
		Files:    tree(),
		Units:    units,
		Groups:   groups,
		Firewall: "table inet filter {}\n",
	}.Validate())
}

func keysOf(groups map[string]Group) []string {
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}

	return names
}
