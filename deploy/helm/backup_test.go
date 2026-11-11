// Copyright 2026 The Taisce Authors
// SPDX-License-Identifier: Apache-2.0

package helm_test

import (
	"os/exec"
	"strings"
	"testing"
)

// renderDefaults is `helm template` with nothing stated at all, which the shared render helper no
// longer is: that helper states that there is no backup, because a release that says nothing about
// one is refused and nearly every other test wants a rendering to read.
func renderDefaults(args ...string) (string, error) {
	out, err := exec.Command("helm", append([]string{"template", "t", "taisce"}, args...)...).CombinedOutput()
	return string(out), err
}

// A release states where its backups go, or says it has none.
//
// Three instances and a failover path protect against losing a node. They do not protect against
// anything that is replicated — an erasure of the wrong subject, a migration that goes wrong, a
// dropped schema, an uninstall — because each of those is on every instance within a second. The
// chart rendered a highly available database with no backup and said nothing about it, so an
// operator arrived at "no backup" by not choosing.
//
// Refused at render, like a release that over-declares its connections: this is cheaper to correct
// before anything is applied than to discover on the day something has to be recovered.
func TestAReleaseStatesWhereItsBackupsGoOrSaysItHasNone(t *testing.T) {
	// Nothing stated: refused, and the message names both ways out.
	out, err := renderDefaults()
	if err == nil {
		t.Fatal("a release that says nothing about backups rendered")
	}
	for _, want := range []string{"postgresql.cnpg.backup.objectStore", "postgresql.cnpg.backup.acknowledgedNone", "Replicas protect"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal must say what to set and why replicas are not the answer; it lacks %q:\n%s", want, out)
		}
	}

	// Stated as none: renders, with nothing that archives and nothing scheduled.
	none, err := renderDefaults("--set", "postgresql.cnpg.backup.acknowledgedNone=true")
	if err != nil {
		t.Fatalf("a release that says it has no backup was refused: %v\n%s", err, none)
	}
	for _, absent := range []string{"kind: ScheduledBackup", "barman-cloud.cloudnative-pg.io", "isWALArchiver"} {
		if strings.Contains(none, absent) {
			t.Errorf("a release with no backup rendered %q", absent)
		}
	}

	// Stated as an object store: the cluster archives to it and a base backup is scheduled.
	stored, err := renderDefaults("--set", "postgresql.cnpg.backup.objectStore=memory-backups")
	if err != nil {
		t.Fatalf("a release that names an object store was refused: %v\n%s", err, stored)
	}
	for _, want := range []string{
		"name: barman-cloud.cloudnative-pg.io",
		"isWALArchiver: true",
		`barmanObjectName: "memory-backups"`,
		"kind: ScheduledBackup",
		"method: plugin",
		`schedule: "0 0 2 * * *"`,
		// A first backup when the schedule is created, not at the next two in the morning: a
		// deployment whose only backup is tomorrow's has none today.
		"immediate: true",
		"name: t-taisce-db",
	} {
		if !strings.Contains(stored, want) {
			t.Errorf("a release that names an object store lacks %q", want)
		}
	}
	// The schedule is the operator's to state.
	if hourly, err := renderDefaults("--set", "postgresql.cnpg.backup.objectStore=memory-backups",
		"--set-string", "postgresql.cnpg.backup.schedule=0 0 * * * *"); err != nil || !strings.Contains(hourly, `schedule: "0 0 * * * *"`) {
		t.Errorf("a stated schedule was not rendered: %v", err)
	}

	// Both at once is a contradiction, and guessing which was meant is how a deployment ends up
	// with the one its operator did not intend.
	if both, err := renderDefaults("--set", "postgresql.cnpg.backup.objectStore=memory-backups",
		"--set", "postgresql.cnpg.backup.acknowledgedNone=true"); err == nil || !strings.Contains(both, "both") {
		t.Errorf("a release that names an object store and also says it has no backup was not refused: %v\n%s", err, both)
	}

	// An external database is somebody else's to back up, and this chart never reads its settings.
	external, err := renderDefaults("--set", "postgresql.mode=external", "--set", "postgresql.external.existingSecret=db")
	if err != nil {
		t.Fatalf("an external database was asked about a backup this chart cannot configure: %v\n%s", err, external)
	}
	if strings.Contains(external, "kind: ScheduledBackup") {
		t.Error("an external database was given a backup schedule")
	}
}

// The notes say which of the two a release chose, where an operator reads them once.
//
// A release with no backup is permitted and must not be quiet about it: the sentence is the only
// thing between "chose none" and "forgot it had none" six months later.
func TestTheInstallNotesSayWhetherThereIsABackup(t *testing.T) {
	none := notes(t, "--set", "postgresql.cnpg.backup.acknowledgedNone=true")
	if !strings.Contains(none, "NO BACKUP") || !strings.Contains(none, "cannot be recovered") {
		t.Errorf("the notes for a release with no backup do not say so:\n%s", none)
	}
	stored := notes(t, "--set", "postgresql.cnpg.backup.objectStore=memory-backups")
	if strings.Contains(stored, "NO BACKUP") {
		t.Error("the notes for a release with a backup say it has none")
	}
	for _, want := range []string{"memory-backups", "get backup", "restore"} {
		if !strings.Contains(stored, want) {
			t.Errorf("the notes for a release with a backup lack %q:\n%s", want, stored)
		}
	}
}
