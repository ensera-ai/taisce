// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package deploy_test

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// Compose takes a project's name from the file when the file sets one, and from the directory when it
// does not. The name decides which containers and which volume a command acts on. Fixed in a shipped
// file, it makes every copy of that file on a machine one stack: `up` in a second directory recreates
// the first install's containers against the first install's volume, bootstrap fails on that volume's
// password, and `down -v` in either directory deletes the other's data (#73, D12).
//
// Every shipped file is checked, overlays included, because an overlay that sets `name` renames the
// whole merged project just as the base file would. What this does not cover: a name passed with `-p`
// or COMPOSE_PROJECT_NAME, which is a choice the person running the command made, and which the
// measurement overlays and the GPU qualification scripts rely on deliberately.
func TestNoShippedComposeFileFixesTheProjectName(t *testing.T) {
	for _, file := range []string{"../compose.yaml", "../compose.build.yaml", "../compose.perf.yaml", "../compose.gpu.yaml"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var top map[string]any
		if err := yaml.Unmarshal(raw, &top); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if len(top) == 0 {
			t.Fatalf("%s parsed to nothing, so this asserts nothing about it", file)
		}
		if name, fixed := top["name"]; fixed {
			t.Errorf("%s fixes the project name to %v, so every copy of it on one machine is one stack", file, name)
		}
	}
}
