// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package deploy_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type composeService struct {
	Image      string `yaml:"image"`
	Build      any    `yaml:"build"`
	PullPolicy string `yaml:"pull_policy"`
}

func composeServices(t *testing.T, file string) map[string]composeService {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Services map[string]composeService `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	if len(parsed.Services) == 0 {
		t.Fatalf("%s parsed to no services, so this asserts nothing about it", file)
	}
	return parsed.Services
}

// A working-tree build never lands on a release's tag.
//
// The local image cache is one namespace for every Compose install on a machine. A build written to
// `ghcr.io/ensera-ai/taisce:<release>` replaces that release for all of them, and the next install
// recreated anywhere runs the build while every name it shows says release. So every service the
// base file runs from a published image is overridden here with a tag that carries no registry host,
// which no release pushes and no registry can serve, and with `pull_policy: build`, so Compose builds
// it rather than asking a registry for it.
//
// What this does not cover: two checkouts on one machine share the local tag, so the last one built is
// what either runs. Both are working-tree builds, which is the distinction this exists to keep.
func TestTheBuildOverlayNeverTagsARelease(t *testing.T) {
	base := composeServices(t, "../compose.yaml")
	overlay := composeServices(t, "../compose.build.yaml")

	checked := 0
	for name, service := range base {
		if !strings.HasPrefix(service.Image, "ghcr.io/ensera-ai/") {
			continue
		}
		checked++
		built, ok := overlay[name]
		if !ok {
			t.Errorf("%s runs %s and the overlay does not build it, so a working-tree run mixes a release into it", name, service.Image)
			continue
		}
		if built.Build == nil {
			t.Errorf("%s: the overlay names it without building it", name)
		}
		if built.Image == "" {
			t.Errorf("%s: the overlay builds it without naming a tag, so the build lands on the base file's %s", name, service.Image)
			continue
		}
		if repository := strings.SplitN(built.Image, ":", 2)[0]; strings.Contains(repository, "/") {
			t.Errorf("%s: the overlay tags its build %s, a name a registry can serve or a release can use", name, built.Image)
		}
		if built.PullPolicy != "build" {
			t.Errorf("%s: pull_policy is %q, so Compose may pull %s instead of building it", name, built.PullPolicy, built.Image)
		}
	}
	if checked == 0 {
		t.Fatal("compose.yaml names no published image, so this asserts nothing")
	}
}
