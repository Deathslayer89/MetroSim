package main

import (
	"errors"
	"testing"

	"github.com/twmb/franz-go/pkg/kadm"
)

func TestParsePartitionOffsets(t *testing.T) {
	got, err := parsePartitionOffsets("trip.completed:0:1200, trip.completed:3:980,ns:topic:1:5")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		topic     string
		partition int32
		at        int64
	}{{"trip.completed", 0, 1200}, {"trip.completed", 3, 980}, {"ns:topic", 1, 5}} {
		o, ok := got.Lookup(want.topic, want.partition)
		if !ok || o.At != want.at {
			t.Errorf("%s p%d: got %+v (found %v), want offset %d", want.topic, want.partition, o, ok, want.at)
		}
	}
	for _, bad := range []string{"trip.completed:0", "trip.completed:x:1", "trip.completed:0:-1", ":0:1", "t:0:1,t:0:2", "garbage"} {
		if _, err := parsePartitionOffsets(bad); err == nil {
			t.Errorf("%q parsed without error", bad)
		}
	}
}

func TestCheckInRangeRejectsOffsetsOutsideTheRetainedLog(t *testing.T) {
	listed := func(at int64) kadm.ListedOffsets {
		return kadm.ListedOffsets{"t": {0: {Topic: "t", Partition: 0, Offset: at}}}
	}
	starts, ends := listed(100), listed(500)
	for at, ok := range map[int64]bool{99: false, 100: true, 300: true, 500: true, 501: false} {
		want := make(kadm.Offsets)
		want.AddOffset("t", 0, at, -1)
		if err := checkInRange(want, starts, ends); (err == nil) != ok {
			t.Errorf("offset %d: err %v, want ok=%v", at, err, ok)
		}
	}
	missing := make(kadm.Offsets)
	missing.AddOffset("t", 7, 200, -1)
	if err := checkInRange(missing, starts, ends); err == nil {
		t.Error("a partition the broker doesn't have was accepted")
	}
	failed := kadm.ListedOffsets{"t": {0: {Topic: "t", Partition: 0, Err: errors.New("not leader")}}}
	ok := make(kadm.Offsets)
	ok.AddOffset("t", 0, 200, -1)
	if err := checkInRange(ok, failed, ends); err == nil {
		t.Error("a partition whose listing failed was accepted")
	}
}
