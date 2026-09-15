// Copyright Savely Krasovsky 2026
// SPDX-License-Identifier: MPL-2.0

package deployment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActivationPolicyValidation(t *testing.T) {
	for name, change := range map[string]func(*Payload){
		"conflicting modes":   func(p *Payload) { p.TryRestart = []string{"app.service"} },
		"duplicate restart":   func(p *Payload) { p.Restart = append(p.Restart, "app.service") },
		"template activation": func(p *Payload) { p.Restart = []string{"app@.service"} },
		"non-native enable":   func(p *Payload) { p.Enable = []string{"app.service"} },
	} {
		t.Run(name, func(t *testing.T) {
			payload := Payload{
				Files:   map[string]string{"containers/systemd/app.container": "[Container]\nImage=app\n"},
				Restart: []string{"app.service"},
			}
			require.NoError(t, payload.Validate())
			change(&payload)
			require.Error(t, payload.Validate())
		})
	}
}
