package moonbit

import (
	"strings"
	"testing"
	"time"
)

// TestTheRefusalIsReadEvenWhenItArrivesLate is the guard for the reason a
// run never started. sudo's refusal arrives on stderr while the run's own
// output closes on stdout, and the two pipes close independently: the reason
// has to be read after the watcher is done with the pipe, not before.
func TestTheRefusalIsReadEvenWhenItArrivesLate(t *testing.T) {
	tail := &lastLine{}
	done := make(chan struct{})
	go func() {
		time.Sleep(50 * time.Millisecond)
		tail.add([]byte("moonbit may not run moonbit with sudo\n"))
		close(done)
	}()

	err := refusalReason(done, tail)
	if err == nil || !strings.Contains(err.Error(), "may not run moonbit with sudo") {
		t.Fatalf("err = %v, want the refusal", err)
	}
}

// TestAWatcherThatNeverFinishesStillGivesAnAnswer bounds the wait: a pipe
// that stays open must not hold the reason back.
func TestAWatcherThatNeverFinishesStillGivesAnAnswer(t *testing.T) {
	grace := stderrGrace
	stderrGrace = 20 * time.Millisecond
	t.Cleanup(func() { stderrGrace = grace })

	tail := &lastLine{}
	tail.add([]byte("moonbit may not run moonbit with sudo\n"))

	err := refusalReason(make(chan struct{}), tail)
	if err == nil || !strings.Contains(err.Error(), "may not run moonbit with sudo") {
		t.Fatalf("err = %v, want the refusal", err)
	}
}

// TestNoWordsAtAllStillGiveAReason keeps the fallback message: a run that
// says nothing must not leave the panel with an empty error.
func TestNoWordsAtAllStillGiveAReason(t *testing.T) {
	done := make(chan struct{})
	close(done)

	err := refusalReason(done, &lastLine{})
	if err == nil || !strings.Contains(err.Error(), "exited before it started") {
		t.Fatalf("err = %v, want the fallback reason", err)
	}
}

// Start reads the refusal through a pipe the same process reaps, and Wait
// closes that pipe. Draining the watcher before Wait is what keeps sudo's
// words; this repeats the run because the ordering only loses them sometimes.
func TestSudoRefusalSurvivesRepeatedRuns(t *testing.T) {
	for i := 0; i < 100; i++ {
		r := fakeSudo(t, "refused")
		_, err := r.Start([]byte("hunter2"), Request{Cmd: "scan"})
		if err == nil || !strings.Contains(err.Error(), "may not run moonbit with sudo") {
			t.Fatalf("run %d: err = %v, want sudo's refusal", i, err)
		}
	}
}
