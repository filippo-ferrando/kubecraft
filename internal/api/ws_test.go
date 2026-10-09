package api

import (
	"strings"
	"testing"
	"time"
)

func TestReadLogs(t *testing.T) {
	var got []string
	send := func(m termMsg) bool { got = append(got, m.Line); return true }
	var last time.Time

	in := "2026-10-09T14:45:49.000000001Z first\n2026-10-09T14:45:50.000000001Z second\n"
	if res := readLogs(strings.NewReader(in), &last, send); res != logsEnded || len(got) != 2 {
		t.Fatalf("res %v lines %v", res, got)
	}
	// A second read with overlapping lines only sends new ones.
	in = "2026-10-09T14:45:50.000000001Z second\n2026-10-09T14:45:51.000000001Z third\n"
	readLogs(strings.NewReader(in), &last, send)
	if strings.Join(got, ",") != "first,second,third" {
		t.Fatalf("lines: %v", got)
	}

	// The kubelet's inotify error switches to polling and is not shown.
	in = "2026-10-09T14:45:52.000000001Z fourth\nfailed to create fsnotify watcher: too many open files\n"
	if res := readLogs(strings.NewReader(in), &last, send); res != logsCannotFollow {
		t.Fatalf("expected cannot follow, got %v", res)
	}
	if got[len(got)-1] != "fourth" {
		t.Fatalf("error line leaked: %v", got)
	}

	// A closed socket stops reading.
	stop := func(termMsg) bool { return false }
	if res := readLogs(strings.NewReader("2026-10-09T14:45:59Z x\n"), &last, stop); res != logsClientGone {
		t.Fatalf("expected client gone, got %v", res)
	}
}
