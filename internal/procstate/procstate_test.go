package procstate

import (
	"os"
	"os/exec"
	"testing"
)

func TestAliveRejectsNonPositivePIDs(t *testing.T) {
	for _, pid := range []int{0, -1, -99999} {
		if Alive(pid) {
			t.Errorf("Alive(%d) = true, want false", pid)
		}
	}
}

func TestAliveReportsOurselves(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Fatal("Alive(os.Getpid()) = false, want true for the running test process")
	}
}

// TestAliveReportsAReapedProcess pins the answer the prepared-home sweep depends on: a process that has exited AND been waited for must read as dead, or an orphaned session home is pinned on disk forever.
func TestAliveReportsAReapedProcess(t *testing.T) {
	// -test.run=^$ matches no test, so the child binary starts, runs nothing and exits 0 straight away.
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run short-lived helper: %v", err)
	}
	pid := cmd.Process.Pid
	if Alive(pid) {
		t.Fatalf("Alive(%d) = true for a process that exited and was reaped", pid)
	}
}
