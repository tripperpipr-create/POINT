package main

import (
	"context"
	"path/filepath"
	"testing"
)

func TestReleaseSoakProfilesCannotWeakenObjective(t *testing.T) {
	for _, name := range []string{"eight-hour", "twenty-four-hour"} {
		profile, faults, err := readProfile(filepath.Join("..", "..", "distribution", "soak-profile.json"), name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !profile.ReleaseQualifying || profile.FixtureFiles < 50_000 || profile.Events < 100_000 ||
			profile.Runs < 5_000 || profile.FlowExecutions < 2_000 || profile.ConcurrentFullRuns < 4 ||
			profile.DesktopProbeEverySec > 3600 || profile.HubCyclesPerProbe < 2 || len(faults) < 9 {
			t.Fatalf("%s is weaker than the production workload: %#v faults=%v", name, profile, faults)
		}
	}
}

func TestDiskLowGuardRefusesBeforeWriting(t *testing.T) {
	refused, err := exerciseNearDiskLimitRefusal(t.TempDir())
	if err != nil || !refused {
		t.Fatalf("refused=%v err=%v", refused, err)
	}
}

func TestTemporaryDiskWriteLossRecovers(t *testing.T) {
	denied, recovered, err := exerciseTemporaryDiskWriteLoss(context.Background(), t.TempDir())
	if err != nil || !denied || !recovered {
		t.Fatalf("denied=%v recovered=%v err=%v", denied, recovered, err)
	}
}
