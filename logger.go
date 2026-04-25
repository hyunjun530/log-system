package main

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
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

const maxLogSize = 10 * 1024 * 1024 // 10MB

type Store struct {
	mu   sync.RWMutex
	path string
}

func NewStore(path string) *Store {
	return &Store{path: path}
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

	if info, err := os.Stat(s.path); err == nil && info.Size() >= maxLogSize {
		if renameErr := os.Rename(s.path, s.path+".1"); renameErr != nil {
			log.Printf("log rotation failed (%s → %s.1): %v", s.path, s.path, renameErr)
		}
	}

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(append(data, '\n'))
	return err
}

type reverseScanner struct {
	file *os.File
	buf  []byte
	pos  int64
	tail []byte
}

func newReverseScanner(f *os.File) (*reverseScanner, error) {
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return &reverseScanner{
		file: f,
		buf:  make([]byte, 64*1024),
		pos:  stat.Size(),
	}, nil
}

func (r *reverseScanner) Scan() ([]byte, bool) {
	for {
		if i := bytes.LastIndexByte(r.tail, '\n'); i >= 0 {
			line := r.tail[i+1:]
			r.tail = r.tail[:i]
			if len(line) > 0 {
				return line, true
			}
			continue
		}

		if r.pos == 0 {
			if len(r.tail) > 0 {
				line := r.tail
				r.tail = nil
				return line, true
			}
			return nil, false
		}

		readSize := int64(len(r.buf))
		if r.pos < readSize {
			readSize = r.pos
		}
		r.pos -= readSize

		if _, err := r.file.Seek(r.pos, os.SEEK_SET); err != nil {
			return nil, false
		}
		n, err := r.file.Read(r.buf[:readSize])
		if err != nil && n == 0 {
			return nil, false
		}
		if n > 0 {
			newTail := make([]byte, n+len(r.tail))
			copy(newTail, r.buf[:n])
			copy(newTail[n:], r.tail)
			r.tail = newTail
		}
	}
}

func (s *Store) Query(levels []string, q string, from, to time.Time, page, size int) QueryResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if page < 1 {
		page = 1
	}
	if size < 0 {
		size = 0
	}

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

	// Scan newest-first: current file, then rotated .1. We do not early-terminate
	// once `items` is full — we keep counting matches so `Total` is accurate.
	// File size is bounded by rotation (maxLogSize), so a full scan is cheap.
	for _, path := range []string{s.path, s.path + ".1"} {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner, err := newReverseScanner(f)
		if err != nil {
			f.Close()
			continue
		}
		for {
			line, ok := scanner.Scan()
			if !ok {
				break
			}
			if len(line) == 0 {
				continue
			}
			var e Entry
			if err := json.Unmarshal(line, &e); err != nil {
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
		f.Close()
	}

	return QueryResult{Items: items, Page: page, Size: size, Total: matchCount}
}
