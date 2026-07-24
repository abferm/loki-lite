package journal

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Journal reads a journald log across multiple journal files (active + archived).
// Files are sorted by HeadEntrySeqnum (smallest = oldest). The last file in the
// list is the active file receiving new entries. Handles file rotation, gap
// detection across files, and header reload when the active file grows.
//
// Not safe for concurrent use. Each goroutine should open its own Journal.
type Journal struct {
	dir        string
	name       string
	files      []*Reader
	entry      *Entry
	activeIdx  int
	tailSeqnum uint64
}

// OpenJournal opens all journal files matching name in dir. Files are opened as
// name.journal (active) and name@*.journal (archived), sorted by HeadEntrySeqnum.
// Returns error if no matching files exist or any file fails to open.
func OpenJournal(dir, name string) (*Journal, error) {
	var files []string

	pattern := filepath.Join(dir, name+"*.journal")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob journal files: %w", err)
	}

	for _, path := range matches {
		base := filepath.Base(path)
		if base == name+".journal" || strings.HasPrefix(base, name+"@") {
			files = append(files, path)
		}
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no journal files found for %q in %s", name, dir)
	}

	type fileInfo struct {
		path         string
		headSeqnum   uint64
		tailRealtime uint64
	}

	var infos []fileInfo
	for _, path := range files {
		r, err := Open(path)
		if err != nil {
			for _, f := range infos {
				r, err := Open(f.path)
				if err == nil {
					r.Close()
				}
			}
			return nil, fmt.Errorf("failed to open %s: %w", path, err)
		}
		infos = append(infos, fileInfo{
			path:         path,
			headSeqnum:   r.HeadEntrySeqnum(),
			tailRealtime: r.TailEntryRealtime(),
		})
		r.Close()
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].headSeqnum < infos[j].headSeqnum
	})

	var readers []*Reader
	for _, info := range infos {
		r, err := Open(info.path)
		if err != nil {
			for _, reader := range readers {
				reader.Close()
			}
			return nil, fmt.Errorf("failed to open %s: %w", info.path, err)
		}
		readers = append(readers, r)
	}

	var activeIdx int
	if len(readers) > 0 {
		activeIdx = len(readers) - 1
	}

	var tailSeqnum uint64
	if len(readers) > 0 {
		tailSeqnum = readers[activeIdx].TailEntrySeqnum()
	}

	return &Journal{
		dir:        dir,
		name:       name,
		files:      readers,
		activeIdx:  activeIdx,
		tailSeqnum: tailSeqnum,
	}, nil
}

// SeekRealtime positions the journal at the first entry whose timestamp is >= t.
// Files entirely before t are skipped. The best file (smallest HeadEntryRealtime
// that covers t) is SeekRealtime'd and its first entry is read into Entry().
// Call Next() after SeekRealtime to continue iteration.
func (j *Journal) SeekRealtime(t time.Time) {
	usec := uint64(t.UnixMicro())

	j.entry = nil

	var bestFile *Reader
	for _, r := range j.files {
		if r.TailEntryRealtime() < usec {
			r.offset = r.size
			r.entry = nil
		} else {
			if bestFile == nil || r.HeadEntryRealtime() < bestFile.HeadEntryRealtime() {
				bestFile = r
			}
		}
	}

	if bestFile != nil {
		bestFile.SeekRealtime(t)
		if e, ok := bestFile.NextEntry(); ok {
			j.entry = e
		}
	}

	for _, r := range j.files {
		if r != bestFile {
			r.offset = r.size
			r.entry = nil
		}
	}
}

// SeekHead resets all files to their first entry and clears the current Entry.
// The next Next() call reads from the oldest entry across all files.
func (j *Journal) SeekHead() {
	for _, r := range j.files {
		r.SeekHead()
	}
	j.entry = nil
}

// NextEntry is a convenience wrapper: calls Next() then returns (Entry(), true)
// on success, or (nil, false) if no more entries.
func (j *Journal) NextEntry() (e *Entry, ok bool) {
	ok = j.Next()
	if ok {
		e = j.entry
	}
	return
}

// Next advances to the next entry in sequence-number order across all files.
// On first call (Entry() == nil), reads the first entry from the file with the
// smallest HeadEntrySeqnum. On subsequent calls, advances within the current file
// or jumps to the next file. Handles gaps between files by finding the file with
// the smallest HeadEntrySeqnum greater than the current seqnum. When caught up,
// reloads the active file's header to detect rotation or new entries. Returns
// false when no more entries are available.
func (j *Journal) Next() bool {
	if len(j.files) == 0 {
		return false
	}

	if j.entry == nil {
		var first *Reader
		for _, r := range j.files {
			if first == nil || r.HeadEntrySeqnum() < first.HeadEntrySeqnum() {
				first = r
			}
		}
		first.SeekHead()
		if entry, ok := first.NextEntry(); ok {
			j.entry = entry
			return true
		}
		return false
	}

	for {
		currentSeqnum := j.entry.Seqnum()
		nextSeqnum := currentSeqnum + 1

		var currentFile, nextFile *Reader
		for _, r := range j.files {
			if r.containsSeqnum(currentSeqnum) {
				currentFile = r
			}
			if r.containsSeqnum(nextSeqnum) {
				nextFile = r
			}
		}

		if currentFile != nil && nextFile != nil && currentFile == nextFile {
			if entry, ok := currentFile.NextEntry(); ok {
				j.entry = entry
				return true
			}
			return false
		}

		activeFile := j.files[j.activeIdx]

		if nextFile == nil && nextSeqnum < activeFile.HeadEntrySeqnum() {
			for _, r := range j.files {
				if r.HeadEntrySeqnum() > currentSeqnum {
					if nextFile == nil || r.HeadEntrySeqnum() < nextFile.HeadEntrySeqnum() {
						nextFile = r
					}
				}
			}
		}

		if nextFile != nil {
			nextFile.SeekHead()
			if entry, ok := nextFile.NextEntry(); ok {
				j.entry = entry
				return true
			}
			return false
		}

		if err := activeFile.ReloadHeader(); err != nil {
			return false
		}

		if activeFile.State() == stateArchived {
			j.cleanupDeletedFiles()
			j.openNewActiveFile()
			if len(j.files) > 0 {
				j.tailSeqnum = j.files[j.activeIdx].TailEntrySeqnum()
			}
			continue
		}

		if activeFile.TailEntrySeqnum() > j.tailSeqnum {
			j.tailSeqnum = activeFile.TailEntrySeqnum()
			continue
		}

		return false
	}
}

func (j *Journal) cleanupDeletedFiles() {
	for i := len(j.files) - 1; i >= 0; i-- {
		if !j.files[i].Exists() {
			j.files[i].Close()
			j.files = append(j.files[:i], j.files[i+1:]...)
			if j.activeIdx >= i {
				if j.activeIdx > 0 {
					j.activeIdx--
				} else {
					j.activeIdx = 0
				}
			}
		}
	}
}

func (j *Journal) openNewActiveFile() {
	expectedPath := filepath.Join(j.dir, j.name+".journal")
	r, err := Open(expectedPath)
	if err == nil {
		j.files = append(j.files, r)
		j.activeIdx = len(j.files) - 1
	}
}

// Entry returns the current entry, or nil if no entry has been read yet.
func (j *Journal) Entry() *Entry {
	return j.entry
}

// Close closes all underlying Readers. Returns the first error encountered;
// remaining files are still closed.
func (j *Journal) Close() error {
	for _, r := range j.files {
		if err := r.Close(); err != nil {
			return err
		}
	}
	return nil
}

// Follow polls for new entries every 10ms and calls fn for each entry. Returns
// true (stopped early) if fn returns false or ctx is cancelled. Returns false if
// ctx is cancelled while waiting for new entries. Useful for tailing live journals:
//
//	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
//	defer stop()
//	j.Follow(ctx, func(e *Entry) bool {
//	    fmt.Println(e)
//	    return true // keep following
//	})
func (j *Journal) Follow(ctx context.Context, fn func(*Entry) bool) bool {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		for entry, ok := j.NextEntry(); ok && ctx.Err() == nil; entry, ok = j.NextEntry() {
			if !fn(entry) {
				return true
			}
		}

		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// NFiles returns the number of open journal files (active + archived).
func (j *Journal) NFiles() int {
	return len(j.files)
}

// Files returns the open Readers sorted by HeadEntrySeqnum (index 0 = oldest).
// Useful for diagnostics (e.g. printing seqnum ranges with cmd/inspect).
func (j *Journal) Files() []*Reader {
	return j.files
}
