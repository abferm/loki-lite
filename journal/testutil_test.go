package journal

import (
	"encoding/binary"
	"os"
	"sort"
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

type testFieldEntry struct {
	seqnum   uint64
	realtime uint64
	fields   map[string]string
}

type testJournalWithFieldsOpts struct {
	state   uint8
	entries []testFieldEntry
}

func writeTestJournalWithFields(t *testing.T, path string, opts testJournalWithFieldsOpts) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	if len(opts.entries) == 0 {
		t.Fatal("writeTestJournalWithFields: must have at least one entry")
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

	// First pass: collect all unique field names and data objects per field.
	fieldMap := make(map[string][]uint64) // field name -> data object offsets
	fileID := [16]byte{}                 // zero file ID for test data

	// Write header placeholder, then entries, then data, then field objects, then field hash table.
	var content []byte
	content = append(content, make([]byte, headerSize)...)
	copy(content[0:8], []byte(journalMagic))

	// Write entries and their data objects.
	entryOffsets := make([]uint64, 0, len(opts.entries))
	for _, e := range opts.entries {
		// Align entry to 8 bytes.
		if len(content)%8 != 0 {
			content = append(content, make([]byte, 8-len(content)%8)...)
		}

		entryOff := uint64(len(content))

		// Build entry items: each field gets one 16-byte item pointing to its data object.
		type entryItem struct {
			dataOffset uint64
		}
		var items []entryItem
		for key, val := range e.fields {
			// Write data object.
			payload := []byte(key + "=" + val)
			// Data object: objectHeader(16) + hash(8) + next_hash_offset(8) + next_field_offset(8) + entry_offset(8) + entry_array_offset(8) + n_entries(8) + payload
			const dataObjHeader = 16 + 8 + 8 + 8 + 8 + 8 + 8 // 64
			dObjSize := uint64(dataObjHeader) + uint64(len(payload))
			dObj := make([]byte, dObjSize)
			dObj[0] = objectData
			binary.LittleEndian.PutUint64(dObj[8:16], dObjSize)
			copy(dObj[dataObjHeader:], payload)

			// Align data object to 8 bytes.
			if len(content)%8 != 0 {
				content = append(content, make([]byte, 8-len(content)%8)...)
			}
			dOff := uint64(len(content))
			content = append(content, dObj...)

			items = append(items, entryItem{dataOffset: dOff})
			fieldMap[key] = append(fieldMap[key], dOff)
		}

		// Now write the entry object with items.
		// Align entry to 8 bytes.
		if len(content)%8 != 0 {
			content = append(content, make([]byte, 8-len(content)%8)...)
		}
		entryOff = uint64(len(content))

		entryObjSize := uint64(64) + uint64(len(items)*16)
		entryObj := make([]byte, entryObjSize)
		entryObj[0] = objectEntry
		binary.LittleEndian.PutUint64(entryObj[8:16], entryObjSize)
		binary.LittleEndian.PutUint64(entryObj[16:24], e.seqnum)
		binary.LittleEndian.PutUint64(entryObj[24:32], e.realtime)

		// Write entry items (data offset + hash pairs).
		for i, item := range items {
			itemOff := 64 + i*16
			binary.LittleEndian.PutUint64(entryObj[itemOff:itemOff+8], item.dataOffset)
		}

		content = append(content, entryObj...)
		entryOffsets = append(entryOffsets, entryOff)
	}

	// Chain data objects by field name via next_field_offset.
	for _, offsets := range fieldMap {
		for i := 0; i < len(offsets)-1; i++ {
			off := offsets[i]
			// next_field_offset is at byte 32 within the data object (after objectHeader=16 + hash=8 + next_hash_offset=8)
			nextFieldFileOff := off + 32
			// We need to write into the content buffer. The data objects are already in content.
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
		fo[0] = objectField
		binary.LittleEndian.PutUint64(fo[8:16], foSize)
		binary.LittleEndian.PutUint64(fo[16:24], SipHash24(fileID, []byte(name)))
		// next_hash_offset = 0 (no collision chain for test data)
		if len(fieldMap[name]) > 0 {
			binary.LittleEndian.PutUint64(fo[32:40], fieldMap[name][0])
		}
		copy(fo[40:], payload)
		content = append(content, fo...)
	}

	// Build field hash table, patching content for collision chains.
	fieldTable, fieldTableSize := buildTestFieldHashTable(fieldNames, fileID, fieldObjOffsets, content)
	fieldTableAbsOff := uint64(len(content))
	content = append(content, fieldTable...)

	// Fix up header.
	binary.LittleEndian.PutUint64(content[88:96], headerSize)
	binary.LittleEndian.PutUint64(content[120:128], fieldTableAbsOff)          // FieldTableOffset
	binary.LittleEndian.PutUint64(content[128:136], fieldTableSize)            // FieldTableSize
	binary.LittleEndian.PutUint64(content[136:144], entryOffsets[len(entryOffsets)-1]) // TailObjectOffset
	binary.LittleEndian.PutUint64(content[144:152], uint64(len(opts.entries)+len(fieldNames))) // NObjects
	binary.LittleEndian.PutUint64(content[152:160], uint64(len(opts.entries))) // NEntries
	binary.LittleEndian.PutUint64(content[160:168], tailSeqnum)
	binary.LittleEndian.PutUint64(content[168:176], headSeqnum)
	binary.LittleEndian.PutUint64(content[184:192], headRealtime)
	binary.LittleEndian.PutUint64(content[192:200], tailRealtime)
	binary.LittleEndian.PutUint64(content[216:224], uint64(len(fieldNames)))   // NFields
	content[16] = opts.state

	if _, err := f.Write(content); err != nil {
		t.Fatalf("write content: %v", err)
	}
}

func buildTestFieldHashTable(fieldNames []string, fileID [16]byte, fieldObjOffsets map[string]uint64, content []byte) ([]byte, uint64) {
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
		hash := SipHash24(fileID, []byte(name))
		slot := hash % numSlots
		off := slot * 16

		foff := fieldObjOffsets[name]

		if binary.LittleEndian.Uint64(table[off:off+8]) == 0 {
			binary.LittleEndian.PutUint64(table[off:off+8], foff)
			binary.LittleEndian.PutUint64(table[off+8:off+16], foff)
		} else {
			tailOff := binary.LittleEndian.Uint64(table[off+8 : off+16])
			// Update tail's next_hash_offset. next_hash_offset is at byte 24 in the field object.
			// The field object is in the content buffer at offset tailOff.
			nextHashContentOff := tailOff + 24
			binary.LittleEndian.PutUint64(content[nextHashContentOff:nextHashContentOff+8], foff)
			binary.LittleEndian.PutUint64(table[off+8:off+16], foff)
		}
	}

	return table, tableSize
}
