package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func msg(s string) json.RawMessage { return json.RawMessage(s) }

func TestAppendAndQuery(t *testing.T) {
	f, _ := os.CreateTemp("", "test-*.jsonl")
	f.Close()
	defer os.Remove(f.Name())

	store := NewStore(f.Name())

	now := time.Now().UnixMilli()
	if err := store.Append(now-100, "INFO", msg(`{"event":"start","service":"api"}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(now-50, "ERROR", msg(`{"event":"fail","reason":"timeout"}`)); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(now, "INFO", msg(`{"event":"deploy","version":"1.2.3"}`)); err != nil {
		t.Fatal(err)
	}

	zero := time.Time{}

	// all entries
	res := store.Query(nil, "", zero, zero, 1, 50)
	if res.Total != 3 {
		t.Fatalf("expected total 3, got %d", res.Total)
	}
	// newest first
	if !strings.Contains(string(res.Items[0].Message), "deploy") {
		t.Fatalf("expected newest first, got %s", res.Items[0].Message)
	}

	// filter by single level
	res = store.Query([]string{"ERROR"}, "", zero, zero, 1, 50)
	if res.Total != 1 || res.Items[0].Level != "ERROR" {
		t.Fatalf("level filter failed: %+v", res)
	}

	// filter by multiple levels
	res = store.Query([]string{"INFO", "ERROR"}, "", zero, zero, 1, 50)
	if res.Total != 3 {
		t.Fatalf("multi-level filter failed: expected 3, got %d", res.Total)
	}

	// filter by keyword (searches within JSON string)
	res = store.Query(nil, "timeout", zero, zero, 1, 50)
	if res.Total != 1 || !strings.Contains(string(res.Items[0].Message), "timeout") {
		t.Fatalf("keyword filter failed: %+v", res)
	}

	// pagination
	res = store.Query(nil, "", zero, zero, 2, 2)
	if res.Total != 3 || len(res.Items) != 1 {
		t.Fatalf("pagination failed: total=%d items=%d", res.Total, len(res.Items))
	}

	// page out of range
	res = store.Query(nil, "", zero, zero, 99, 50)
	if len(res.Items) != 0 {
		t.Fatalf("expected empty items for out-of-range page")
	}
}

func TestQueryMissingFile(t *testing.T) {
	store := NewStore("/tmp/nonexistent-log-system-test.jsonl")
	res := store.Query(nil, "", time.Time{}, time.Time{}, 1, 50)
	if res.Total != 0 || res.Items == nil {
		t.Fatalf("expected empty result for missing file, got %+v", res)
	}
}

func TestQueryTimeRange(t *testing.T) {
	f, _ := os.CreateTemp("", "test-time-*.jsonl")
	f.Close()
	defer os.Remove(f.Name())

	store := NewStore(f.Name())
	now := time.Now().UnixMilli()
	store.Append(now-10, "INFO", msg(`{"event":"old"}`))
	time.Sleep(2 * time.Millisecond)
	before := time.Now().UTC()
	time.Sleep(2 * time.Millisecond)
	store.Append(time.Now().UnixMilli(), "WARN", msg(`{"event":"target"}`))
	time.Sleep(2 * time.Millisecond)
	after := time.Now().UTC()
	time.Sleep(2 * time.Millisecond)
	store.Append(time.Now().UnixMilli(), "INFO", msg(`{"event":"new"}`))

	// only the middle entry falls within [before, after]
	res := store.Query(nil, "", before, after, 1, 50)
	if res.Total != 1 || !strings.Contains(string(res.Items[0].Message), "target") {
		t.Fatalf("time range filter failed: total=%d items=%v", res.Total, res.Items)
	}

	// open-ended: from=before should include target + new (2 entries)
	res = store.Query(nil, "", before, time.Time{}, 1, 50)
	if res.Total != 2 {
		t.Fatalf("open-ended from filter failed: expected 2, got %d", res.Total)
	}
}
