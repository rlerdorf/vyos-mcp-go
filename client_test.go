package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestParseBatchValid(t *testing.T) {
	ops, err := parseBatch([]map[string]any{
		{"op": "set", "path": []any{"system", "host-name", "vyos"}},
		{"op": "delete", "path": []any{"service", "https"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 2 || ops[0].cmd != mySet || ops[1].cmd != myDelete {
		t.Fatalf("wrong commands: %+v", ops)
	}
	if strings.Join(ops[0].path, " ") != "system host-name vyos" {
		t.Fatalf("wrong path: %v", ops[0].path)
	}
}

// Every malformed entry must be caught before anything is applied, wherever it
// sits in the list.
func TestParseBatchRejectsBeforeApplying(t *testing.T) {
	good := map[string]any{"op": "set", "path": []any{"a", "b"}}
	cases := map[string]map[string]any{
		"unknown op":        {"op": "rename", "path": []any{"a"}},
		"missing op":        {"path": []any{"a"}},
		"missing path":      {"op": "set"},
		"empty path":        {"op": "set", "path": []any{}},
		"path not an array": {"op": "set", "path": "a b"},
		"non-string elem":   {"op": "set", "path": []any{"a", 5.0}},
	}
	for name, bad := range cases {
		ops, err := parseBatch([]map[string]any{good, good, bad})
		if err == nil || ops != nil {
			t.Errorf("%s: want error and no ops, got ops=%v err=%v", name, ops, err)
			continue
		}
		if !strings.HasPrefix(err.Error(), "operation 3:") {
			t.Errorf("%s: error should name operation 3, got %q", name, err)
		}
	}
}

// Only exit 1 means "no changes"; any other failure must surface as an error,
// or Commit reports "no staged changes" and BatchConfigure misreports what a
// rollback discarded.
func TestSessionChangedResult(t *testing.T) {
	run := func(ctx context.Context, code string) error {
		return exec.CommandContext(ctx, "sh", "-c", "exit "+code).Run()
	}
	bg := context.Background()

	if changed, err := sessionChangedResult(run(bg, "0")); !changed || err != nil {
		t.Errorf("exit 0: want (true, nil), got (%v, %v)", changed, err)
	}
	if changed, err := sessionChangedResult(run(bg, "1")); changed || err != nil {
		t.Errorf("exit 1: want (false, nil), got (%v, %v)", changed, err)
	}
	if _, err := sessionChangedResult(run(bg, "2")); err == nil {
		t.Error("exit 2: want an error, got nil")
	}
	cancelled, cancel := context.WithCancel(bg)
	cancel()
	if _, err := sessionChangedResult(run(cancelled, "0")); err == nil {
		t.Error("cancelled context: want an error, got nil")
	}
	if _, err := sessionChangedResult(exec.Command("/nonexistent/cli-shell-api").Run()); err == nil {
		t.Error("missing binary: want an error, got nil")
	}
}

// The rollback context must outlive a cancelled request.
func TestDiscardContextSurvivesCancelledRequest(t *testing.T) {
	req, cancel := context.WithCancel(context.Background())
	cancel()
	cleanup, stop := context.WithTimeout(context.WithoutCancel(req), discardTimeout)
	defer stop()
	if err := exec.CommandContext(cleanup, "true").Run(); err != nil {
		t.Fatalf("cleanup command did not run after request cancellation: %v", err)
	}
}
