package engine

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/abferm/loki-lite/journal"
)

type testEntry struct {
	seqnum   uint64
	realtime uint64
	fields   map[string]string
}

func writeTestJournal(t *testing.T, dir, name string, entries []testEntry) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	if len(entries) == 0 {
		t.Fatal("writeTestJournal: must have at least one entry")
	}

	var headSeqnum, tailSeqnum uint64
	var headRealtime, tailRealtime uint64
	headSeqnum = entries[0].seqnum
	tailSeqnum = entries[0].seqnum
	headRealtime = entries[0].realtime
	tailRealtime = entries[0].realtime
	for _, e := range entries[1:] {
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

	// Collect all unique field names and data object offsets.
	fieldMap := make(map[string][]uint64)
	fileID := [16]byte{}

	var content []byte
	content = append(content, make([]byte, headerSize)...)
	copy(content[0:8], []byte("LPKSHHRH"))

	entryOffsets := make([]uint64, 0, len(entries))
	for _, e := range entries {
		if len(content)%8 != 0 {
			content = append(content, make([]byte, 8-len(content)%8)...)
		}

		type entryItem struct {
			dataOffset uint64
		}
		var items []entryItem
		for key, val := range e.fields {
			payload := []byte(key + "=" + val)
			const dataObjHeader = 16 + 8 + 8 + 8 + 8 + 8 + 8
			dObjSize := uint64(dataObjHeader) + uint64(len(payload))
			dObj := make([]byte, dObjSize)
			dObj[0] = 1 // objectData
			binary.LittleEndian.PutUint64(dObj[8:16], dObjSize)
			copy(dObj[dataObjHeader:], payload)

			if len(content)%8 != 0 {
				content = append(content, make([]byte, 8-len(content)%8)...)
			}
			dOff := uint64(len(content))
			content = append(content, dObj...)

			items = append(items, entryItem{dataOffset: dOff})
			fieldMap[key] = append(fieldMap[key], dOff)
		}

		if len(content)%8 != 0 {
			content = append(content, make([]byte, 8-len(content)%8)...)
		}

		entryObjSize := uint64(64) + uint64(len(items)*16)
		entryObj := make([]byte, entryObjSize)
		entryObj[0] = 3 // objectEntry
		binary.LittleEndian.PutUint64(entryObj[8:16], entryObjSize)
		binary.LittleEndian.PutUint64(entryObj[16:24], e.seqnum)
		binary.LittleEndian.PutUint64(entryObj[24:32], e.realtime)

		for i, item := range items {
			itemOff := 64 + i*16
			binary.LittleEndian.PutUint64(entryObj[itemOff:itemOff+8], item.dataOffset)
		}

		entryOff := uint64(len(content))
		content = append(content, entryObj...)
		entryOffsets = append(entryOffsets, entryOff)
	}

	// Chain data objects by field name.
	for _, offsets := range fieldMap {
		for i := 0; i < len(offsets)-1; i++ {
			nextFieldFileOff := offsets[i] + 32
			if nextFieldFileOff+8 <= uint64(len(content)) {
				binary.LittleEndian.PutUint64(content[nextFieldFileOff:nextFieldFileOff+8], offsets[i+1])
			}
		}
	}

	// Build field objects.
	fieldNames := make([]string, 0, len(fieldMap))
	for name := range fieldMap {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)

	fieldObjOffsets := make(map[string]uint64)
	for _, name := range fieldNames {
		if len(content)%8 != 0 {
			content = append(content, make([]byte, 8-len(content)%8)...)
		}
		fieldObjOffsets[name] = uint64(len(content))

		payload := []byte(name + "\x00")
		foSize := uint64(16 + 8 + 8 + 8 + len(payload))
		fo := make([]byte, foSize)
		fo[0] = 2 // objectField
		binary.LittleEndian.PutUint64(fo[8:16], foSize)
		binary.LittleEndian.PutUint64(fo[16:24], journal.SipHash24(fileID, []byte(name)))
		if len(fieldMap[name]) > 0 {
			binary.LittleEndian.PutUint64(fo[32:40], fieldMap[name][0])
		}
		copy(fo[40:], payload)
		content = append(content, fo...)
	}

	// Build field hash table.
	fieldTable, fieldTableSize := buildFieldHashTable(fieldNames, fileID, fieldObjOffsets, content)
	fieldTableAbsOff := uint64(len(content))
	content = append(content, fieldTable...)

	// Fix up header.
	binary.LittleEndian.PutUint64(content[88:96], headerSize)
	binary.LittleEndian.PutUint64(content[120:128], fieldTableAbsOff)
	binary.LittleEndian.PutUint64(content[128:136], fieldTableSize)
	binary.LittleEndian.PutUint64(content[136:144], entryOffsets[len(entryOffsets)-1])
	binary.LittleEndian.PutUint64(content[144:152], uint64(len(entries)+len(fieldNames)))
	binary.LittleEndian.PutUint64(content[152:160], uint64(len(entries)))
	binary.LittleEndian.PutUint64(content[160:168], tailSeqnum)
	binary.LittleEndian.PutUint64(content[168:176], headSeqnum)
	binary.LittleEndian.PutUint64(content[184:192], headRealtime)
	binary.LittleEndian.PutUint64(content[192:200], tailRealtime)
	binary.LittleEndian.PutUint64(content[216:224], uint64(len(fieldNames)))
	content[16] = 1 // stateOnline

	if _, err := f.Write(content); err != nil {
		t.Fatalf("write content: %v", err)
	}
}

func buildFieldHashTable(fieldNames []string, fileID [16]byte, fieldObjOffsets map[string]uint64, content []byte) ([]byte, uint64) {
	if len(fieldNames) == 0 {
		return nil, 0
	}

	numSlots := uint64(1)
	for numSlots < uint64(len(fieldNames)) {
		numSlots *= 2
	}

	tableSize := numSlots * 16
	table := make([]byte, tableSize)

	for _, name := range fieldNames {
		hash := journal.SipHash24(fileID, []byte(name))
		slot := hash % numSlots
		off := slot * 16

		foff := fieldObjOffsets[name]

		if binary.LittleEndian.Uint64(table[off:off+8]) == 0 {
			binary.LittleEndian.PutUint64(table[off:off+8], foff)
			binary.LittleEndian.PutUint64(table[off+8:off+16], foff)
		} else {
			tailOff := binary.LittleEndian.Uint64(table[off+8 : off+16])
			nextHashContentOff := tailOff + 24
			binary.LittleEndian.PutUint64(content[nextHashContentOff:nextHashContentOff+8], foff)
			binary.LittleEndian.PutUint64(table[off+8:off+16], foff)
		}
	}

	return table, tableSize
}

func TestSeriesEmpty(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir, "test.journal", []testEntry{
		{seqnum: 1, realtime: 1000000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
	})

	j, err := journal.OpenJournal(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	eng := New(j, nil)

	// Empty filters returns nil.
	result, err := eng.Series(nil, time.Unix(0, 0), time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}

func TestSeriesMatchAll(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir, "test.journal", []testEntry{
		{seqnum: 1, realtime: 1000000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
		{seqnum: 2, realtime: 2000000, fields: map[string]string{"job": "nginx", "MESSAGE": "world"}},
	})

	j, err := journal.OpenJournal(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	eng := New(j, nil)

	result, err := eng.Series([]any{"all"}, time.Unix(0, 0), time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 label sets, got %d: %v", len(result), result)
	}

	// Verify label sets contain expected keys.
	keys := make(map[string]bool)
	for _, ls := range result {
		keys[ls["job"]] = true
	}
	if !keys["sshd"] || !keys["nginx"] {
		t.Fatalf("expected sshd and nginx, got %v", keys)
	}
}

func TestSeriesTimeRange(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir, "test.journal", []testEntry{
		{seqnum: 1, realtime: 1000000, fields: map[string]string{"job": "a"}},    // 1 second
		{seqnum: 2, realtime: 5000000, fields: map[string]string{"job": "b"}},    // 5 seconds
		{seqnum: 3, realtime: 10000000, fields: map[string]string{"job": "c"}},   // 10 seconds
	})

	j, err := journal.OpenJournal(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	eng := New(j, nil)

	// Query 2s to 6s — should match only job=b.
	result, err := eng.Series([]any{"all"}, time.Unix(2, 0), time.Unix(6, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 label set, got %d: %v", len(result), result)
	}
	if result[0]["job"] != "b" {
		t.Fatalf("expected job=b, got %v", result[0])
	}
}

func TestSeriesDeduplication(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir, "test.journal", []testEntry{
		{seqnum: 1, realtime: 1000000, fields: map[string]string{"job": "sshd", "MESSAGE": "one"}},
		{seqnum: 2, realtime: 2000000, fields: map[string]string{"job": "sshd", "MESSAGE": "two"}},
	})

	j, err := journal.OpenJournal(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	eng := New(j, []string{"MESSAGE"})

	result, err := eng.Series([]any{"all"}, time.Unix(0, 0), time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	// Both entries have the same label set {job: sshd} after excluding MESSAGE — should dedup to 1.
	if len(result) != 1 {
		t.Fatalf("expected 1 label set after dedup, got %d: %v", len(result), result)
	}
}

func TestIndexStatsMatchAll(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir, "test.journal", []testEntry{
		{seqnum: 1, realtime: 1000000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
		{seqnum: 2, realtime: 2000000, fields: map[string]string{"job": "nginx", "MESSAGE": "world!"}},
	})

	j, err := journal.OpenJournal(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	eng := New(j, nil)

	stats, err := eng.IndexStats("all", time.Unix(0, 0), time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Entries != 2 {
		t.Errorf("expected 2 entries, got %d", stats.Entries)
	}
	if stats.Streams != 2 {
		t.Errorf("expected 2 streams, got %d", stats.Streams)
	}
	if stats.Bytes != int64(len("hello")+len("world!")) {
		t.Errorf("expected %d bytes, got %d", len("hello")+len("world!"), stats.Bytes)
	}
	if stats.Chunks != 1 {
		t.Errorf("expected 1 chunk (1 file), got %d", stats.Chunks)
	}
}

func TestIndexStatsTimeRange(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir, "test.journal", []testEntry{
		{seqnum: 1, realtime: 1000000, fields: map[string]string{"job": "a", "MESSAGE": "x"}},
		{seqnum: 2, realtime: 5000000, fields: map[string]string{"job": "b", "MESSAGE": "yy"}},
		{seqnum: 3, realtime: 10000000, fields: map[string]string{"job": "c", "MESSAGE": "zzz"}},
	})

	j, err := journal.OpenJournal(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	eng := New(j, nil)

	// Query 2s to 6s — only entry at 5s.
	stats, err := eng.IndexStats("all", time.Unix(2, 0), time.Unix(6, 0))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Entries != 1 {
		t.Errorf("expected 1 entry, got %d", stats.Entries)
	}
	if stats.Bytes != 2 {
		t.Errorf("expected 2 bytes (yy), got %d", stats.Bytes)
	}
}

func TestIndexStatsDeduplicatesStreams(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir, "test.journal", []testEntry{
		{seqnum: 1, realtime: 1000000, fields: map[string]string{"job": "sshd", "MESSAGE": "one"}},
		{seqnum: 2, realtime: 2000000, fields: map[string]string{"job": "sshd", "MESSAGE": "two"}},
	})

	j, err := journal.OpenJournal(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	eng := New(j, []string{"MESSAGE"})

	stats, err := eng.IndexStats("all", time.Unix(0, 0), time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Entries != 2 {
		t.Errorf("expected 2 entries, got %d", stats.Entries)
	}
	// Both have same label set {job: sshd} after excluding MESSAGE — should count as 1 stream.
	if stats.Streams != 1 {
		t.Errorf("expected 1 stream, got %d", stats.Streams)
	}
}

func TestLabels(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir, "test.journal", []testEntry{
		{seqnum: 1, realtime: 1000000, fields: map[string]string{"job": "sshd", "PRIORITY": "4", "MESSAGE": "hello"}},
	})

	j, err := journal.OpenJournal(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	eng := New(j, []string{"MESSAGE"})

	labels, err := eng.Labels()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(labels)

	expected := []string{"PRIORITY", "job"}
	if len(labels) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, labels)
	}
	for i := range expected {
		if labels[i] != expected[i] {
			t.Fatalf("expected %v, got %v", expected, labels)
		}
	}
}

func TestLabelValues(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir, "test.journal", []testEntry{
		{seqnum: 1, realtime: 1000000, fields: map[string]string{"job": "sshd", "PRIORITY": "4"}},
		{seqnum: 2, realtime: 2000000, fields: map[string]string{"job": "nginx", "PRIORITY": "6"}},
	})

	j, err := journal.OpenJournal(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	eng := New(j, nil)

	vals, err := eng.LabelValues("job")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(vals)

	expected := []string{"nginx", "sshd"}
	if len(vals) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, vals)
	}
	for i := range expected {
		if vals[i] != expected[i] {
			t.Fatalf("expected %v, got %v", expected, vals)
		}
	}
}

func TestLabelValuesExcluded(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir, "test.journal", []testEntry{
		{seqnum: 1, realtime: 1000000, fields: map[string]string{"job": "sshd", "MESSAGE": "hello"}},
	})

	j, err := journal.OpenJournal(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()

	eng := New(j, []string{"MESSAGE"})

	_, err = eng.LabelValues("MESSAGE")
	if err != ErrLabelExcluded {
		t.Fatalf("expected ErrLabelExcluded, got %v", err)
	}
}
