package main

import (
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
