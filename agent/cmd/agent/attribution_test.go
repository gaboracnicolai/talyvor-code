package main

import (
	"reflect"
	"testing"
)

// TestSurvivingAttributions — the survival gate, level 2: keep only the last-writer
// output_ids whose file actually SURVIVED into the committed diff. A file edited then
// reverted (absent from the committed diff) is dropped; unknown ids are dropped; the
// result is distinct + deterministic.
func TestSurvivingAttributions(t *testing.T) {
	editAttr := map[string]string{
		"foo.go":  "oid-B", // last writer for foo (oid-A already superseded at loop level)
		"bar.go":  "oid-A",
		"baz.go":  "oid-C", // edited but NOT in the committed diff (reverted) → dropped
		"empt.go": "",      // unknown output_id → dropped
	}
	committed := []string{"foo.go", "bar.go", "empt.go"} // baz.go reverted, not committed

	got := survivingAttributions(editAttr, committed)
	want := []string{"oid-A", "oid-B"} // distinct + sorted; oid-C (reverted) + "" excluded
	if !reflect.DeepEqual(got, want) {
		t.Errorf("survivingAttributions = %v, want %v", got, want)
	}
}

// TestSurvivingAttributions_RevertDropsAll — a file whose only edit was reverted yields
// no attribution (the whole point of the committed-diff gate).
func TestSurvivingAttributions_RevertDropsAll(t *testing.T) {
	if got := survivingAttributions(map[string]string{"x.go": "oid-X"}, []string{}); len(got) != 0 {
		t.Errorf("a reverted file must yield no attribution; got %v", got)
	}
}

// TestSurvivingAttributions_Deterministic — the same input yields the same order.
func TestSurvivingAttributions_Deterministic(t *testing.T) {
	e := map[string]string{"a.go": "z", "b.go": "y", "c.go": "x"}
	c := []string{"a.go", "b.go", "c.go"}
	first := survivingAttributions(e, c)
	for i := 0; i < 5; i++ {
		if !reflect.DeepEqual(survivingAttributions(e, c), first) {
			t.Fatal("survivingAttributions must be deterministic")
		}
	}
}
