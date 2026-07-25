package journal

import (
	"encoding/binary"
	"os"
	"testing"
)

type testEntry struct {
	seqnum   uint64
	realtime uint64
}

type testJournalOpts struct {
	state   uint8
	entries []testEntry
}

func writeTestJournal(t *testing.T, path string, opts testJournalOpts) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	if len(opts.entries) == 0 {
		t.Fatal("writeTestJournal: must have at least one entry")
	}

	var headSeqnum, tailSeqnum uint64
	var headRealtime, tailRealtime uint64
	headSeqnum = opts.entries[0].seqnum
	tailSeqnum = opts.entries[0].seqnum
	headRealtime = opts.entries[0].realtime
	tailRealtime = opts.entries[0].realtime
	for _, e := range opts.entries[1:] {
		if e.seqnum < headSeqnum {
			headSeqnum = e.seqnum
		}
		if e.seqnum > tailSeqnum {
			tailSeqnum = e.seqnum
		}
		if e.realtime < headRealtime {
			headRealtime = e.realtime
		}
		if e.realtime > tailRealtime {
			tailRealtime = e.realtime
		}
	}

	const headerSize uint64 = 240
	const entryObjSize uint64 = 64

	hdr := make([]byte, headerSize)
	copy(hdr[0:8], []byte(journalMagic))
	hdr[16] = opts.state // remaining[8] = State → file offset 16
	// remaining starts at file offset 8, so field offset = remaining_idx + 8
	binary.LittleEndian.PutUint64(hdr[88:96], headerSize)             // HeaderSize
	binary.LittleEndian.PutUint64(hdr[144:152], uint64(len(opts.entries))) // NObjects
	binary.LittleEndian.PutUint64(hdr[152:160], uint64(len(opts.entries))) // NEntries
	binary.LittleEndian.PutUint64(hdr[160:168], tailSeqnum)           // TailEntrySeqnum
	binary.LittleEndian.PutUint64(hdr[168:176], headSeqnum)           // HeadEntrySeqnum
	binary.LittleEndian.PutUint64(hdr[176:184], headerSize)           // EntryArrayOffset
	binary.LittleEndian.PutUint64(hdr[184:192], headRealtime)         // HeadEntryRealtime
	binary.LittleEndian.PutUint64(hdr[192:200], tailRealtime)         // TailEntryRealtime

	if _, err := f.Write(hdr); err != nil {
		t.Fatalf("write header: %v", err)
	}

	for _, e := range opts.entries {
		obj := make([]byte, entryObjSize)
		obj[0] = objectEntry
		binary.LittleEndian.PutUint64(obj[8:16], entryObjSize)
		binary.LittleEndian.PutUint64(obj[16:24], e.seqnum)
		binary.LittleEndian.PutUint64(obj[24:32], e.realtime)

		if _, err := f.Write(obj); err != nil {
			t.Fatalf("write entry: %v", err)
		}
	}
}
