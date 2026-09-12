package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The gate's two promises about Docker's disk, held by running the real script against a
// stand-in `docker` that records every call and reports whatever free space the test chooses. No
// Docker is needed to run this, so it runs inside the gate as well as outside it.
//
// Without room, the gate refuses before it creates anything: a test database that runs out of disk
// mid-run fails every package after it, which reads as hundreds of broken tests. With room, it gets
// past the check — and whatever it started, its exit removes the database together with the volume
// the substrate image declares, which is what used to be left behind on every run.
func TestTheGateRefusesADiskWithoutRoomAndTakesItsDatabaseVolumeWithIt(t *testing.T) {
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	fake := t.TempDir()
	calls := filepath.Join(fake, "calls")
	// Answers df with the free space asked for, refuses to create the network so a run with room stops
	// right after the check, and succeeds at everything else — cleanup included.
	stub := `#!/bin/sh
echo "$*" >> "` + calls + `"
case "$*" in
  *" df "*) printf 'Filesystem 1024-blocks Used Available Capacity Mounted\n/dev/vdb1 100 1 %s 1%% /\n' "$FAKE_FREE_KB" ;;
  "network create"*) exit 1 ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(fake, "docker"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	gate := func(freeKB string) (string, string, error) {
		t.Helper()
		_ = os.Remove(calls)
		cmd := exec.Command("bash", "scripts/gate.sh")
		cmd.Dir = strings.TrimSpace(string(top))
		cmd.Env = append(os.Environ(), "PATH="+fake+":"+os.Getenv("PATH"), "HOME="+t.TempDir(),
			"FAKE_FREE_KB="+freeKB, "GATE_MIN_FREE_GB=6")
		out, err := cmd.CombinedOutput()
		log, _ := os.ReadFile(calls)
		return string(out), string(log), err
	}

	out, log, err := gate("1024")
	if err == nil || !strings.Contains(out, "GiB free and a run needs 6") {
		t.Fatalf("a disk with 1 MiB free was gated: %v\n%s", err, out)
	}
	if strings.Contains(log, "network create") || strings.Contains(log, "run -d") {
		t.Fatalf("the gate started something on a disk it had refused:\n%s", log)
	}

	_, log, _ = gate(strings.Repeat("9", 9))
	if !strings.Contains(log, "network create") {
		t.Fatalf("a disk with room did not get past the check:\n%s", log)
	}
	if !strings.Contains(log, "rm -f -v taisce-gate-db-") {
		t.Fatalf("the gate's exit did not remove its database with its volume:\n%s", log)
	}
}
