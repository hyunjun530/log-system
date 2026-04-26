package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func msg(s string) json.RawMessage { return json.RawMessage(s) }

func TestAppendAndQuery(t *testing.T) {
	f, _ := os.CreateTemp("", "test-*.jsonl")
	f.Close()
	defer os.Remove(f.Name())

	store := NewStore(f.Name(), 0, 0)

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
	store := NewStore("/tmp/nonexistent-log-system-test.jsonl", 0, 0)
	res := store.Query(nil, "", time.Time{}, time.Time{}, 1, 50)
	if res.Total != 0 || res.Items == nil {
		t.Fatalf("expected empty result for missing file, got %+v", res)
	}
}

func TestQueryTimeRange(t *testing.T) {
	f, _ := os.CreateTemp("", "test-time-*.jsonl")
	f.Close()
	defer os.Remove(f.Name())

	store := NewStore(f.Name(), 0, 0)
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

func TestRotationMultiFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.jsonl")
	store := NewStore(logPath, 256, 3)

	for i := 0; i < 30; i++ {
		body := fmt.Sprintf(`{"i":%d,"pad":"%s"}`, i, strings.Repeat("x", 50))
		if err := store.Append(int64(i), "INFO", msg(body)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	for _, suffix := range []string{".1", ".2", ".3"} {
		if _, err := os.Stat(logPath + suffix); err != nil {
			t.Errorf("expected %s to exist: %v", logPath+suffix, err)
		}
	}
	if _, err := os.Stat(logPath + ".4"); err == nil {
		t.Errorf("expected %s to NOT exist (retain=3)", logPath+".4")
	}

	res := store.Query(nil, "", time.Time{}, time.Time{}, 1, 200)
	if res.Total < 4 {
		t.Errorf("expected entries spread across rotated files, got total=%d", res.Total)
	}
	for i := 1; i < len(res.Items); i++ {
		if res.Items[i-1].Timestamp < res.Items[i].Timestamp {
			t.Errorf("results not newest-first at index %d: %d < %d",
				i, res.Items[i-1].Timestamp, res.Items[i].Timestamp)
			break
		}
	}
	// newest entry must be present
	if res.Items[0].Timestamp != 29 {
		t.Errorf("expected newest ts=29, got %d", res.Items[0].Timestamp)
	}
}

func TestOpenQueryFilesSnapshotSurvivesRotation(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.jsonl")
	store := NewStore(logPath, 1024, 1)

	writeEntries(t, logPath+".1", Entry{Timestamp: 1, Level: "INFO", Message: msg(`{"file":"old-rotated"}`)})
	writeEntries(t, logPath, Entry{Timestamp: 2, Level: "INFO", Message: msg(`{"file":"current"}`)})

	files := store.openQueryFiles()
	defer closeQueryFiles(files)
	if len(files) != 2 {
		t.Fatalf("expected current and rotated file handles, got %d", len(files))
	}

	if err := os.Remove(logPath + ".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(logPath, logPath+".1"); err != nil {
		t.Fatal(err)
	}
	writeEntries(t, logPath, Entry{Timestamp: 3, Level: "INFO", Message: msg(`{"file":"new-current"}`)})

	got := collectQueryFileTimestamps(t, files)
	want := []int64{2, 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("opened query snapshot changed after rotation: got %v, want %v", got, want)
	}
}

func TestRotationErrorSurfaced(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.jsonl")
	store := NewStore(logPath, 100, 3)

	if err := store.Append(1, "INFO", msg(`{"x":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte(strings.Repeat("x", 200)), 0644); err != nil {
		t.Fatal(err)
	}
	// Block the oldest rotation slot with a non-empty directory so os.Remove fails.
	blockerDir := logPath + ".3"
	if err := os.Mkdir(blockerDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockerDir, "b"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(2, "INFO", msg(`{"y":2}`)); err == nil {
		t.Fatal("expected rotation error to be propagated, got nil")
	}
}

func TestQueryConcurrentWithAppends(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.jsonl")
	store := NewStore(logPath, 1<<24, 3)

	var stopQuery atomic.Bool
	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		for !stopQuery.Load() {
			store.Query(nil, "", time.Time{}, time.Time{}, 1, 50)
		}
	}()

	var wg sync.WaitGroup
	var appendErr atomic.Value
	const workers = 8
	const perWorker = 100
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				if err := store.Append(int64(id*perWorker+i), "INFO", msg(`{"event":"w"}`)); err != nil {
					appendErr.Store(err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	stopQuery.Store(true)
	<-queryDone

	if v := appendErr.Load(); v != nil {
		t.Fatalf("append failed: %v", v)
	}
	res := store.Query(nil, "", time.Time{}, time.Time{}, 1, 2000)
	if res.Total != workers*perWorker {
		t.Errorf("expected %d entries, got %d", workers*perWorker, res.Total)
	}
}

func TestReverseScannerEdges(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{"empty", "", nil},
		{"no trailing newline", "abc", []string{"abc"}},
		{"single line with newline", "abc\n", []string{"abc"}},
		{"two lines", "abc\ndef\n", []string{"def", "abc"}},
		{"trailing newline only", "\n", nil},
		{"chunk-size line", strings.Repeat("x", reverseChunk), []string{strings.Repeat("x", reverseChunk)}},
		{"line spanning chunks", strings.Repeat("y", reverseChunk*2+10), []string{strings.Repeat("y", reverseChunk*2+10)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.CreateTemp("", "rev-*")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(f.Name())
			if _, err := f.WriteString(tc.content); err != nil {
				t.Fatal(err)
			}
			f.Close()

			f2, err := os.Open(f.Name())
			if err != nil {
				t.Fatal(err)
			}
			defer f2.Close()
			sc, err := newReverseScanner(f2)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for {
				line, ok := sc.Scan()
				if !ok {
					break
				}
				got = append(got, string(line))
			}
			if !reflect.DeepEqual(got, tc.want) {
				if len(tc.content) > 200 {
					t.Errorf("got %d lines (lengths=%v), want %d lines (lengths=%v)",
						len(got), lens(got), len(tc.want), lens(tc.want))
				} else {
					t.Errorf("got %q, want %q", got, tc.want)
				}
			}
		})
	}
}

func lens(s []string) []int {
	out := make([]int, len(s))
	for i, x := range s {
		out[i] = len(x)
	}
	return out
}

func writeEntries(t *testing.T, path string, entries ...Entry) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

func collectQueryFileTimestamps(t *testing.T, files []queryFile) []int64 {
	t.Helper()
	var got []int64
	for _, file := range files {
		for {
			line, ok := file.scanner.Scan()
			if !ok {
				break
			}
			var entry Entry
			if err := json.Unmarshal(line, &entry); err != nil {
				t.Fatal(err)
			}
			got = append(got, entry.Timestamp)
		}
	}
	return got
}

func closeQueryFiles(files []queryFile) {
	for _, file := range files {
		file.file.Close()
	}
}
