package tracelog

import "testing"

func TestBoundedSetEvictsOldestAtCapacity(t *testing.T) {
	s := newBoundedSet[int64](10)
	for i := int64(1); i <= 20; i++ {
		s.Add(i)
	}
	if s.Len() != 10 {
		t.Errorf("len at cap: want 10, got %d", s.Len())
	}
	for i := int64(1); i <= 10; i++ {
		if s.Has(i) {
			t.Errorf("id %d should have been evicted", i)
		}
	}
	for i := int64(11); i <= 20; i++ {
		if !s.Has(i) {
			t.Errorf("id %d should still be present", i)
		}
	}
}

func TestBoundedSetIgnoresDuplicates(t *testing.T) {
	s := newBoundedSet[int64](10)
	for i := 0; i < 5; i++ {
		s.Add(42)
	}
	if s.Len() != 1 {
		t.Errorf("repeated Add should not grow: got %d", s.Len())
	}
}

func TestBoundedSetSmallCap(t *testing.T) {
	s := newBoundedSet[int64](0) // clamps to 1
	s.Add(1)
	s.Add(2)
	if s.Has(1) || !s.Has(2) {
		t.Errorf("cap=1 should hold only the most recent: has(1)=%v has(2)=%v", s.Has(1), s.Has(2))
	}
}
