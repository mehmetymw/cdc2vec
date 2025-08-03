package util

import "testing"

func TestConcatenateColumns(t *testing.T) {
	m := map[string]any{"a":"hello","b":"world","c":nil}
	got := ConcatenateColumns(m, []string{"a","c","b"})
	if got != "hello world" {
		t.Fatalf("got %q", got)
	}
}
