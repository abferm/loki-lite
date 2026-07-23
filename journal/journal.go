package journal

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Journal struct {
	files   []*Reader
	current int
	entry   *Entry
}

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
		path        string
		headSeqnum  uint64
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
			path:        path,
			headSeqnum:  r.header.HeadEntrySeqnum,
			tailRealtime: r.header.TailEntryRealtime,
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

	return &Journal{files: readers}, nil
}

func (j *Journal) SeekRealtime(t time.Time) {
	usec := uint64(t.UnixMicro())

	for _, r := range j.files {
		if r.header.HeadEntryRealtime < usec {
			r.offset = r.size
			r.entry = nil
		} else {
			r.SeekRealtime(t)
		}
	}
	j.entry = nil
}

func (j *Journal) SeekHead() {
	for _, r := range j.files {
		r.SeekHead()
	}
	j.entry = nil
}

func (j *Journal) Next() bool {
	var best *Entry
	bestIdx := -1

	for i, r := range j.files {
		if r.entry == nil {
			if !r.Next() {
				continue
			}
		}

		entry := r.Entry()
		if entry == nil {
			continue
		}

		if best == nil || entry.Timestamp.Before(best.Timestamp) {
			best = entry
			bestIdx = i
		}
	}

	if bestIdx == -1 {
		return false
	}

	j.entry = best
	j.files[bestIdx].entry = nil
	return true
}

func (j *Journal) Entry() *Entry {
	return j.entry
}

func (j *Journal) Close() error {
	for _, r := range j.files {
		if err := r.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (j *Journal) NFiles() int {
	return len(j.files)
}

func (j *Journal) Files() []*Reader {
	return j.files
}
