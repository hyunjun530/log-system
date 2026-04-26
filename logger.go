package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

func levelsSet(levels []string) map[string]bool {
	if len(levels) == 0 {
		return nil
	}
	m := make(map[string]bool, len(levels))
	for _, l := range levels {
		m[l] = true
	}
	return m
}

type Entry struct {
	Timestamp int64           `json:"timestamp"`
	Level     string          `json:"level"`
	Message   json.RawMessage `json:"message"`
}

type QueryResult struct {
	Items []Entry `json:"items"`
	Page  int     `json:"page"`
	Size  int     `json:"size"`
	Total int     `json:"total"`
}

const (
	defaultMaxBytes int64 = 10 * 1024 * 1024 // 10MB
	defaultRetain         = 1
)

type Store struct {
	mu       sync.RWMutex
	path     string
	maxBytes int64
	retain   int
}

func NewStore(path string, maxBytes int64, retain int) *Store {
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	if retain < 1 {
		retain = defaultRetain
	}
	return &Store{path: path, maxBytes: maxBytes, retain: retain}
}

func (s *Store) Append(ts int64, level string, message json.RawMessage) error {
	entry := Entry{
		Timestamp: ts,
		Level:     level,
		Message:   message,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.rotate(); err != nil {
		return err
	}

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(append(data, '\n'))
	return err
}

// rotate must be called with s.mu held for writing. POSIX-only assumption:
// os.Rename keeps already-open Query reader fds pointing at the original
// inode, so a Query that opened files just before rotation still reads
// consistent data from the file it opened.
func (s *Store) rotate() error {
	info, err := os.Stat(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Size() < s.maxBytes {
		return nil
	}
	oldest := s.path + "." + strconv.Itoa(s.retain)
	if err := os.Remove(oldest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for i := s.retain; i > 1; i-- {
		from := s.path + "." + strconv.Itoa(i-1)
		to := s.path + "." + strconv.Itoa(i)
		if err := os.Rename(from, to); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Rename(s.path, s.path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

const reverseChunk = 64 * 1024

// reverseScanner reads a file backwards, yielding one line per Scan call.
// It maintains a single growing buffer with live data anchored at the right
// end so prepending a newly-read chunk is just a write into buf[head-rs:head]
// — no per-iteration allocation after the buffer settles.
type reverseScanner struct {
	file *os.File
	buf  []byte
	pos  int64
	head int
	end  int
}

type queryFile struct {
	path    string
	file    *os.File
	scanner *reverseScanner
}

func newReverseScanner(f *os.File) (*reverseScanner, error) {
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	buf := make([]byte, 2*reverseChunk)
	return &reverseScanner{
		file: f,
		buf:  buf,
		pos:  stat.Size(),
		head: len(buf),
		end:  len(buf),
	}, nil
}

func (r *reverseScanner) Scan() ([]byte, bool) {
	for {
		if r.end > r.head {
			if i := bytes.LastIndexByte(r.buf[r.head:r.end], '\n'); i >= 0 {
				line := r.buf[r.head+i+1 : r.end]
				r.end = r.head + i
				if len(line) > 0 {
					return line, true
				}
				continue
			}
		}

		if r.pos == 0 {
			if r.head < r.end {
				line := r.buf[r.head:r.end]
				r.head = r.end
				return line, true
			}
			return nil, false
		}

		readSize := int64(reverseChunk)
		if r.pos < readSize {
			readSize = r.pos
		}
		rs := int(readSize)

		if r.head < rs {
			currentLen := r.end - r.head
			newCap := 2 * len(r.buf)
			if newCap < currentLen+rs {
				newCap = currentLen + rs
			}
			newBuf := make([]byte, newCap)
			copy(newBuf[newCap-currentLen:], r.buf[r.head:r.end])
			r.buf = newBuf
			r.head = newCap - currentLen
			r.end = newCap
		}

		r.pos -= readSize
		if _, err := r.file.Seek(r.pos, io.SeekStart); err != nil {
			return nil, false
		}
		if _, err := io.ReadFull(r.file, r.buf[r.head-rs:r.head]); err != nil {
			return nil, false
		}
		r.head -= rs
	}
}

func (s *Store) openQueryFiles() []queryFile {
	// Open files and record their sizes while holding the read lock. Appends
	// and rotations take the write lock, so the path set cannot shift between
	// opening current and opening rotated files.
	s.mu.RLock()
	defer s.mu.RUnlock()

	files := make([]queryFile, 0, s.retain+1)
	paths := make([]string, 0, s.retain+1)
	paths = append(paths, s.path)
	for i := 1; i <= s.retain; i++ {
		paths = append(paths, s.path+"."+strconv.Itoa(i))
	}

	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner, err := newReverseScanner(f)
		if err != nil {
			f.Close()
			continue
		}
		files = append(files, queryFile{
			path:    path,
			file:    f,
			scanner: scanner,
		})
	}
	return files
}

func (s *Store) Query(levels []string, q string, from, to time.Time, page, size int) QueryResult {
	if page < 1 {
		page = 1
	}
	if size < 0 {
		size = 0
	}

	// Snapshot opened file handles under read lock; release before scanning so
	// concurrent Appends are not blocked by file I/O. POSIX rename keeps
	// already-open fds pointing at the original inode, so a rotation that
	// happens after this point does not corrupt or duplicate the in-progress scan.
	files := s.openQueryFiles()

	lvlSet := levelsSet(levels)
	qLower := strings.ToLower(q)
	start := (page - 1) * size
	items := make([]Entry, 0, size)
	matchCount := 0

	var fromMs, toMs int64
	if !from.IsZero() {
		fromMs = from.UnixMilli()
	}
	if !to.IsZero() {
		toMs = to.UnixMilli()
	}

	for _, file := range files {
		for {
			line, ok := file.scanner.Scan()
			if !ok {
				break
			}
			if len(line) == 0 {
				continue
			}
			var e Entry
			if err := json.Unmarshal(line, &e); err != nil {
				log.Printf("query: skip malformed line in %s: %.120q", file.path, line)
				continue
			}
			if lvlSet != nil && !lvlSet[e.Level] {
				continue
			}
			if qLower != "" && !strings.Contains(strings.ToLower(string(e.Message)), qLower) {
				continue
			}
			if fromMs > 0 && e.Timestamp < fromMs {
				continue
			}
			if toMs > 0 && e.Timestamp > toMs {
				continue
			}

			matchCount++
			if matchCount <= start {
				continue
			}
			if len(items) < size {
				items = append(items, e)
			}
		}
		file.file.Close()
	}

	return QueryResult{Items: items, Page: page, Size: size, Total: matchCount}
}
