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
	files      []*File
	entry      *Entry
	activeIdx  int
	tailSeqnum uint64
}

// OpenJournal opens all journal files matching name in dir. Files are opened as
// name.journal (active) and name@*.journal (archived), sorted by HeadEntrySeqnum.
// Returns error if no matching files exist or any file fails to open.
func OpenJournal(dir, name string) (*Journal, error) {
	var filePaths []string

	pattern := filepath.Join(dir, name+"*.journal")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob journal files: %w", err)
	}

	for _, path := range matches {
		base := filepath.Base(path)
		if base == name+".journal" || strings.HasPrefix(base, name+"@") {
			filePaths = append(filePaths, path)
		}
	}

	if len(filePaths) == 0 {
		return nil, fmt.Errorf("no journal files found for %q in %s", name, dir)
	}

	type fileInfo struct {
		path         string
		headSeqnum   uint64
		tailRealtime uint64
	}

	var infos []fileInfo
	for _, path := range filePaths {
		f, err := Open(path)
		if err != nil {
			for _, fi := range infos {
				f, err := Open(fi.path)
				if err == nil {
					f.Close()
				}
			}
			return nil, fmt.Errorf("failed to open %s: %w", path, err)
		}
		infos = append(infos, fileInfo{
			path:         path,
			headSeqnum:   f.HeadEntrySeqnum(),
			tailRealtime: f.TailEntryRealtime(),
		})
		f.Close()
	}

	sort.Slice(infos, func(i, j int) bool {
		return infos[i].headSeqnum < infos[j].headSeqnum
	})

	var files []*File
	for _, info := range infos {
		r, err := Open(info.path)
		if err != nil {
			for _, f := range files {
				f.Close()
			}
			return nil, fmt.Errorf("failed to open %s: %w", info.path, err)
		}
		files = append(files, r)
	}

	var activeIdx int
	if len(files) > 0 {
		activeIdx = len(files) - 1
	}

	var tailSeqnum uint64
	if len(files) > 0 {
		tailSeqnum = files[activeIdx].TailEntrySeqnum()
	}

	return &Journal{
		dir:        dir,
		name:       name,
		files:      files,
		activeIdx:  activeIdx,
		tailSeqnum: tailSeqnum,
	}, nil
}

// refresh reloads the active file's header and handles rotation. Returns true
// if new entries may be available (tail seqnum advanced or file rotated).
func (j *Journal) refresh() bool {
	activeFile := j.files[j.activeIdx]
	if err := activeFile.ReloadHeader(); err != nil {
		return false
	}

	if activeFile.State() == stateArchived {
		j.cleanupDeletedFiles()
		j.openNewActiveFile()
		if len(j.files) > 0 {
			j.tailSeqnum = j.files[j.activeIdx].TailEntrySeqnum()
		}
		return true
	}

	if activeFile.TailEntrySeqnum() > j.tailSeqnum {
		j.tailSeqnum = activeFile.TailEntrySeqnum()
		return true
	}

	return false
}

// SeekRealtime positions the journal at the first entry whose timestamp is >= t.
// Files entirely before t are skipped. The best file (smallest HeadEntryRealtime
// that covers t) is SeekRealtime'd and its first entry is read into Entry().
// Returns true if the resulting Entry()'s timestamp is >= t.
// Call Next() after SeekRealtime to continue iteration.
func (j *Journal) SeekRealtime(t time.Time) {
	usec := uint64(t.UnixMicro())

	if len(j.files) == 0 {
		panic("SeekRealtime on empty journal")
	}

	// Refresh if target is beyond active file's tail — picks up new entries
	// and detects rotation before we search.
	if usec > j.files[j.activeIdx].TailEntryRealtime() {
		j.refresh()
	}

	j.entry = nil

	// Find the best file: the one with the smallest HeadEntryRealtime that
	// still covers the target. Skip files whose tail is entirely before t.
	var bestFile *File
	for _, f := range j.files {
		if f.TailEntryRealtime() < usec {
			f.offset = f.size
			f.entry = nil
		} else {
			if bestFile == nil || f.HeadEntryRealtime() < bestFile.HeadEntryRealtime() {
				bestFile = f
			}
		}
	}

	// Seek within the best file. SeekRealtime reads the first entry >= t.
	if bestFile != nil {
		bestFile.SeekRealtime(t)
		j.entry = bestFile.Entry()
	}

	// Nothing >= t found. Latch at the last entry by seqnum so the journal
	// keeps its place and Next() returns false.
	if j.entry == nil {
		j.SeekTail()
	}
}

// SeekHead resets all files to their first entry and clears the current Entry.
// The next Next() call reads from the oldest entry across all files.
func (j *Journal) SeekHead() {
	for _, f := range j.files {
		f.SeekHead()
	}
	j.entry = nil
}

// Seek positions the journal at the entry with the given seqnum and reads it
// into Entry(). Refreshes if seqnum is beyond the tail. Returns true if the
// resulting Entry()'s seqnum is >= seqnum.
func (j *Journal) Seek(seqnum uint64) bool {
	j.entry = nil

	if seqnum > j.tailSeqnum {
		j.refresh()
	}

	for _, f := range j.files {
		if f.Seek(seqnum) {
			j.entry = f.Entry()
			return true
		}
	}
	return false
}

// SeekTail positions the journal at the last entry (by seqnum) and reads it
// into Entry(). Assumes the active file is the tail file. The next Next()
// call returns false.
func (j *Journal) SeekTail() {
	j.entry = nil
	active := j.files[j.activeIdx]
	active.SeekTail()
	j.entry = active.Entry()
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
		panic("Next on empty journal")
	}

	// First call: start from the file with the oldest entry.
	if j.entry == nil {
		var first *File
		for _, f := range j.files {
			if first == nil || f.HeadEntrySeqnum() < first.HeadEntrySeqnum() {
				first = f
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

		// Locate which file holds the current seqnum and which holds the next.
		var currentFile, nextFile *File
		for _, f := range j.files {
			if f.containsSeqnum(currentSeqnum) {
				currentFile = f
			}
			if f.containsSeqnum(nextSeqnum) {
				nextFile = f
			}
		}

		// Same file contains both — advance within it.
		if currentFile != nil && nextFile != nil && currentFile == nextFile {
			if entry, ok := currentFile.NextEntry(); ok {
				j.entry = entry
				return true
			}
			return false
		}

		activeFile := j.files[j.activeIdx]

		// Gap: next seqnum not in any file. Find the file with the smallest
		// HeadEntrySeqnum greater than current — skips over the gap.
		if nextFile == nil && nextSeqnum < activeFile.HeadEntrySeqnum() {
			for _, f := range j.files {
				if f.HeadEntrySeqnum() > currentSeqnum {
					if nextFile == nil || f.HeadEntrySeqnum() < nextFile.HeadEntrySeqnum() {
						nextFile = f
					}
				}
			}
		}

		// Cross file boundary: seek to head of next file and read.
		if nextFile != nil {
			nextFile.SeekHead()
			if entry, ok := nextFile.NextEntry(); ok {
				j.entry = entry
				return true
			}
			return false
		}

		// Caught up to tail. Reload active header to detect rotation or new
		// entries. If nothing new, we're done.
		if !j.refresh() {
			return false
		}
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

// Close closes all underlying Files. Returns the first error encountered;
// remaining files are still closed.
func (j *Journal) Close() error {
	for _, f := range j.files {
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}

// Follow polls for new entries at the given interval and calls fn for each
// entry. Returns nil if fn returns false (stopped early). Returns the context
// cause if ctx is cancelled. Useful for tailing live journals:
//
//	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
//	defer stop()
//	err := j.Follow(ctx, 10*time.Millisecond, func(e *Entry) bool {
//	    fmt.Println(e)
//	    return true // keep following
//	})
func (j *Journal) Follow(ctx context.Context, pollInterval time.Duration, fn func(*Entry) bool) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		for entry, ok := j.NextEntry(); ok && ctx.Err() == nil; entry, ok = j.NextEntry() {
			if !fn(entry) {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return context.Cause(ctx)
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
func (j *Journal) Files() []*File {
	return j.files
}
