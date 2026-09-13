// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
)

const (
	quadletDir = "containers/systemd/"
	nativeDir  = "systemd/user/"
)

type container struct {
	path    string
	content string
	pod     string
	mounts  []string
}

// Derive reads the rendered configuration and returns the units the deployment
// owns together with the restart groups, reproducing the naming rules of the
// Quadlet generator. Grouping follows the pod: members share namespaces, so a
// change to any of them has to restart the whole pod in one transaction.
// The hash of a group covers every file that reaches it, including the
// configuration it bind mounts from %E.
func Derive(files map[string]string) ([]string, map[string]Group, error) {
	containers := map[string]container{}
	pods := map[string]string{}
	var common, native, generated []string

	for _, file := range slices.Sorted(maps.Keys(files)) {
		content := files[file]
		name := path.Base(file)

		switch {
		case strings.HasSuffix(file, ".container"):
			pod, _ := directive(content, "Pod")
			containers[strings.TrimSuffix(name, ".container")] = container{
				path:    file,
				content: content,
				pod:     pod,
				mounts:  configMounts(content),
			}
		case strings.HasSuffix(file, ".pod"):
			pods[name] = file
		case strings.HasSuffix(file, ".network"):
			generated = append(generated, strings.TrimSuffix(name, ".network")+"-network.service")
		case strings.HasSuffix(file, ".volume"):
			generated = append(generated, strings.TrimSuffix(name, ".volume")+"-volume.service")
		case strings.HasPrefix(file, nativeDir) && strings.HasSuffix(file, ".service"):
			native = append(native, name)
		}

		if strings.HasPrefix(file, quadletDir) &&
			(strings.HasSuffix(file, ".network") || strings.HasSuffix(file, ".volume") || strings.HasSuffix(file, ".conf")) {
			common = append(common, file)
		}
	}

	members := map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(containers)) {
		group := name + ".service"
		if pod := containers[name].pod; pod != "" {
			if _, known := pods[pod]; !known {
				return nil, nil, fmt.Errorf("container %s joins an undeclared pod: %q", name, pod)
			}

			group = strings.TrimSuffix(pod, ".pod") + "-pod.service"
		}

		members[group] = append(members[group], name)
	}

	groups := make(map[string]Group, len(members))
	for group, names := range members {
		paths := slices.Clone(common)
		for _, name := range names {
			member := containers[name]
			paths = append(paths, member.path)
			if member.pod != "" {
				paths = append(paths, pods[member.pod])
			}
			paths = append(paths, mounted(files, member.mounts)...)
		}

		units := []string{group}
		for _, name := range names {
			units = append(units, name+".service")
		}

		groups[group] = Group{
			Units:  distinct(units),
			Enable: []string{},
			Hash:   hashFiles(files, paths),
			UsesSecrets: slices.ContainsFunc(names, func(name string) bool {
				_, found := directive(containers[name].content, "Secret")

				return found
			}),
		}
	}

	if err := deriveTimers(files, groups); err != nil {
		return nil, nil, err
	}

	units := make([]string, 0, len(groups)+len(native)+len(generated))
	for _, group := range slices.Sorted(maps.Keys(groups)) {
		units = append(units, groups[group].Units...)
	}
	units = append(units, slices.Concat(native, generated)...)

	return unique(units), groups, nil
}

// A timer and its service are one unit of change: the timer carries the
// schedule, the service the command, and neither is useful on its own.
func deriveTimers(files map[string]string, groups map[string]Group) error {
	for _, file := range slices.Sorted(maps.Keys(files)) {
		if !strings.HasPrefix(file, nativeDir) || !strings.HasSuffix(file, ".timer") {
			continue
		}

		service, found := files[strings.TrimSuffix(file, ".timer")+".service"]
		if !found {
			return fmt.Errorf("timer has no service: %s", file)
		}

		name := path.Base(file)
		groups[name] = Group{
			Units:  []string{name},
			Enable: []string{name},
			Hash:   Digest(map[string]string{"timer": files[file], "service": service}),
		}
	}

	return nil
}

// directive returns the first value of a systemd key. Quadlet files are rendered
// by this module, so the simple form is the only one that occurs.
func directive(content, key string) (string, bool) {
	for line := range strings.SplitSeq(content, "\n") {
		if value, found := strings.CutPrefix(strings.TrimSuffix(line, "\r"), key+"="); found {
			return value, true
		}
	}

	return "", false
}

func distinct(values []string) []string {
	seen := make(map[string]bool, len(values))
	kept := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			kept = append(kept, value)
		}
	}

	return kept
}

// configMounts lists the paths below %E, the user configuration directory, that
// a container bind mounts. Everything else is application data or a named
// volume, and does not belong in a restart hash.
func configMounts(content string) []string {
	var mounts []string
	for line := range strings.SplitSeq(content, "\n") {
		value, found := strings.CutPrefix(strings.TrimSuffix(line, "\r"), "Volume=%E/")
		if !found {
			continue
		}

		mounts = append(mounts, strings.Split(value, ":")[0])
	}

	return mounts
}

func mounted(files map[string]string, mounts []string) []string {
	var matched []string
	for _, file := range slices.Sorted(maps.Keys(files)) {
		for _, mount := range mounts {
			if file == mount || strings.HasPrefix(file, mount+"/") {
				matched = append(matched, file)

				break
			}
		}
	}

	return matched
}

func hashFiles(files map[string]string, paths []string) string {
	selected := make(map[string]string, len(paths))
	for _, file := range paths {
		selected[file] = files[file]
	}

	return Digest(selected)
}
