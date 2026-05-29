package db

import (
	"testing"
)

func TestObservatoryRevisionsOrdered(t *testing.T) {
	for i, rev := range observatoryRevisions {
		if rev.version != i+1 {
			t.Errorf("revision at index %d has version %d; want %d", i, rev.version, i+1)
		}
		if rev.name == "" {
			t.Errorf("revision %d has empty name", rev.version)
		}
		if len(rev.stmts) == 0 {
			t.Errorf("revision %d (%s) has no statements", rev.version, rev.name)
		}
	}
}

func TestObservatoryRevisionCount(t *testing.T) {
	const want = 5
	if got := len(observatoryRevisions); got != want {
		t.Errorf("len(observatoryRevisions) = %d; want %d", got, want)
	}
}

func TestObservatoryRevisionStmtsNonEmpty(t *testing.T) {
	for _, rev := range observatoryRevisions {
		for i, stmt := range rev.stmts {
			if stmt == "" {
				t.Errorf("revision %d (%s) stmt[%d] is empty", rev.version, rev.name, i)
			}
		}
	}
}
