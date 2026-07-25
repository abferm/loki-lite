//go:build ignore

package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/abferm/loki-lite/journal"
)

const journalMagic = "LPKSHHRH"
const headerSize = 272

const (
	objectData  = 1
	objectField = 2
	objectEntry = 3
)

const (
	incompatibleKeyedHash = 1 << 2
	incompatibleCompact   = 1 << 4
)

func putLE64(buf []byte, off int, v uint64) {
	binary.LittleEndian.PutUint64(buf[off:off+8], v)
}

func putLE32(buf []byte, off int, v uint32) {
	binary.LittleEndian.PutUint32(buf[off:off+4], v)
}

func writeMinimal(dir string) {
	content := make([]byte, headerSize)
	copy(content, journalMagic)
	putLE64(content, 88, headerSize)
	os.WriteFile(filepath.Join(dir, "minimal.journal"), content, 0644)
}

func writeOneEntry(dir string) {
	payload1 := []byte("__REALTIME_TIMESTAMP=1234567890000000")
	payload2 := []byte("MESSAGE=Test message")

	const dataObjFields = 48
	d1Size := uint64(16) + dataObjFields + uint64(len(payload1))
	d2Size := uint64(16) + dataObjFields + uint64(len(payload2))

	entryItemsSize := uint64(16 + 16)
	entryObjSize := uint64(64) + entryItemsSize

	// Header
	content := make([]byte, headerSize)
	copy(content, journalMagic)
	putLE64(content, 88, headerSize)

	// Entry object
	entryOff := uint64(headerSize)
	entryObj := make([]byte, entryObjSize)
	entryObj[0] = objectEntry
	putLE64(entryObj, 8, entryObjSize)
	putLE64(entryObj, 24, 1234567890000000) // realtime
	content = append(content, entryObj...)

	// Data object 1
	d1Off := uint64(len(content))
	d1 := make([]byte, d1Size)
	d1[0] = objectData
	putLE64(d1, 8, d1Size)
	copy(d1[16+dataObjFields:], payload1)
	content = append(content, d1...)

	// Align for data object 2
	d2Off := (d1Off + d1Size + 7) & ^uint64(7)
	if pad := d2Off - (d1Off + d1Size); pad > 0 {
		content = append(content, make([]byte, pad)...)
	}

	// Data object 2
	d2 := make([]byte, d2Size)
	d2[0] = objectData
	putLE64(d2, 8, d2Size)
	copy(d2[16+dataObjFields:], payload2)
	content = append(content, d2...)

	// Patch entry item offsets
	itemOff := int(entryOff + 64)
	putLE64(content, itemOff, d1Off)
	putLE64(content, itemOff+16, d2Off)

	os.WriteFile(filepath.Join(dir, "one_entry.journal"), content, 0644)
}

type entryDef struct {
	seqnum   uint64
	realtime uint64
	fields   map[string]string
}

type dataObjInfo struct {
	key       string
	value     string
	offset    uint64
	entryOff  uint64
}

func writeMultiFile(dir string) {
	entries1 := []entryDef{
		{seqnum: 1, realtime: 1000000, fields: map[string]string{"MESSAGE": "Entry 1", "SYSLOG_IDENTIFIER": "svc1"}},
		{seqnum: 2, realtime: 2000000, fields: map[string]string{"MESSAGE": "Entry 2", "SYSLOG_IDENTIFIER": "svc1"}},
	}
	entries2 := []entryDef{
		{seqnum: 3, realtime: 3000000, fields: map[string]string{"MESSAGE": "Entry 3", "SYSLOG_IDENTIFIER": "svc2"}},
		{seqnum: 4, realtime: 4000000, fields: map[string]string{"MESSAGE": "Entry 4", "SYSLOG_IDENTIFIER": "svc2"}},
	}

	writeJournalFile(filepath.Join(dir, "system.journal"), entries1, 1, 2, 0)
	writeJournalFile(filepath.Join(dir, "system@0000000000000002-0000000000000001.journal"), entries2, 3, 4, 1)
}

func writeJournalFile(path string, entries []entryDef, headSeqnum, tailSeqnum uint64, state uint8) {
	var content []byte

	const dataObjFields = 48

	// First pass: collect all data objects grouped by field name
	type fieldDataObjs struct {
		offsets []uint64
	}
	fieldMap := make(map[string]*fieldDataObjs)

	// Build entries and data objects
	type entryInfo struct {
		offset uint64
		size   uint64
	}
	var entryInfos []entryInfo
	var allDataObjs []dataObjInfo

	// Calculate sizes for entries
	totalEntrySize := uint64(0)
	for _, e := range entries {
		entryItemsSize := uint64(len(e.fields) * 16)
		entryObjSize := uint64(64) + entryItemsSize
		totalEntrySize += entryObjSize
	}

	// Build file: header + entries + data
	fileSize := uint64(headerSize) + totalEntrySize
	content = make([]byte, 0, fileSize)
	content = append(content, make([]byte, headerSize)...)
	copy(content, journalMagic)

	// Write entries
	for _, e := range entries {
		// Align entry object to 8 bytes
		currentLen := uint64(len(content))
		alignedLen := (currentLen + 7) & ^uint64(7)
		if pad := alignedLen - currentLen; pad > 0 {
			content = append(content, make([]byte, pad)...)
		}

		entryOff := uint64(len(content))
		entryItemsSize := uint64(len(e.fields) * 16)
		entryObjSize := uint64(64) + entryItemsSize

		entryObj := make([]byte, entryObjSize)
		entryObj[0] = objectEntry
		putLE64(entryObj, 8, entryObjSize)
		putLE64(entryObj, 16, e.seqnum)
		putLE64(entryObj, 24, e.realtime)
		content = append(content, entryObj...)

		// Write data objects for this entry
		fieldIdx := 0
		for key, val := range e.fields {
			payload := []byte(key + "=" + val)
			dSize := uint64(16) + dataObjFields + uint64(len(payload))

			// Align data object to 8 bytes
			currentLen := uint64(len(content))
			alignedLen := (currentLen + 7) & ^uint64(7)
			if pad := alignedLen - currentLen; pad > 0 {
				content = append(content, make([]byte, pad)...)
			}

			dOff := uint64(len(content))
			dObj := make([]byte, dSize)
			dObj[0] = objectData
			putLE64(dObj, 8, dSize)
			copy(dObj[16+dataObjFields:], payload)
			content = append(content, dObj...)

			// Patch entry item offset
			itemOff := int(entryOff + 64 + uint64(fieldIdx)*16)
			putLE64(content, itemOff, dOff)

			allDataObjs = append(allDataObjs, dataObjInfo{
				key: key, value: val, offset: dOff, entryOff: entryOff,
			})

			if _, ok := fieldMap[key]; !ok {
				fieldMap[key] = &fieldDataObjs{}
			}
			fieldMap[key].offsets = append(fieldMap[key].offsets, dOff)

			fieldIdx++
		}

		entryInfos = append(entryInfos, entryInfo{offset: entryOff, size: entryObjSize})
	}

	// Patch next_field_offset in each data object to chain by field name
	for _, fd := range fieldMap {
		for i := 0; i < len(fd.offsets)-1; i++ {
			// next_field_offset is at byte offset 40 within the data object (after objectHeader=16 + hash=8 + nextHash=8 + nextField=8)
			// But we need to write at the correct position in the content buffer
			// data object layout: [type,flags,reserved[6],size(8)] = 16 bytes objectHeader
			// then: hash(8), next_hash_offset(8), next_field_offset(8), entry_offset(8), entry_array_offset(8), n_entries(8)
			// next_field_offset is at offset 32 within the data object payload (after objectHeader)
			nextFieldOff := int(fd.offsets[i]) + 32
			putLE64(content, nextFieldOff, fd.offsets[i+1])
		}
	}

	// Collect unique field names (sorted for deterministic output)
	fieldNames := make([]string, 0, len(fieldMap))
	for name := range fieldMap {
		fieldNames = append(fieldNames, name)
	}
	sort.Strings(fieldNames)

	// Build field objects
	fileID := [16]byte{} // zero file ID for test data
	var fieldObjects []byte
	fieldObjOffsets := make(map[string]uint64)

	for _, name := range fieldNames {
		// Align field object to 8 bytes
		currentLen := uint64(len(content) + len(fieldObjects))
		alignedLen := (currentLen + 7) & ^uint64(7)
		if pad := alignedLen - currentLen; pad > 0 {
			fieldObjects = append(fieldObjects, make([]byte, pad)...)
		}

		fieldObjOff := uint64(len(content)) + uint64(len(fieldObjects))
		fieldObjOffsets[name] = fieldObjOff

		payload := []byte(name + "\x00") // null-terminated field name
		foSize := uint64(16 + 8 + 8 + 8 + len(payload)) // objectHeader + hash + nextHash + headData + payload

		fo := make([]byte, foSize)
		fo[0] = objectField
		putLE64(fo, 8, foSize)
		// hash
		putLE64(fo, 16, journal.SipHash24(fileID, []byte(name)))
		// next_hash_offset = 0 (no collision chain)
		// head_data_offset = first data object for this field
		putLE64(fo, 32, fieldMap[name].offsets[0])
		copy(fo[40:], payload)

		fieldObjects = append(fieldObjects, fo...)
	}

	// Build field hash table
	fieldTable, fieldTableSize := buildFieldHashTable(fieldNames, fileID, fieldObjOffsets, content)

	// Append field objects and field hash table to content
	fieldTableAbsOff := uint64(len(content)) + uint64(len(fieldObjects))
	content = append(content, fieldObjects...)
	if fieldTable != nil {
		content = append(content, fieldTable...)
	}

	// Fix up header fields
	headEntry := entries[0]
	tailEntry := entries[len(entries)-1]
	lastEntryOff := entryInfos[len(entryInfos)-1].offset

	putLE64(content, 88, headerSize)                                    // HeaderSize
	putLE64(content, 120, fieldTableAbsOff)                             // FieldTableOffset (remaining[112])
	putLE64(content, 128, fieldTableSize)                               // FieldTableSize (remaining[120])
	putLE64(content, 136, lastEntryOff)                                 // TailObjectOffset
	putLE64(content, 144, uint64(len(entries)+len(fieldMap)))           // NObjects (entries + field objects)
	putLE64(content, 152, uint64(len(entries)))                         // NEntries
	putLE64(content, 160, tailEntry.seqnum)                             // TailEntrySeqnum
	putLE64(content, 168, headEntry.seqnum)                             // HeadEntrySeqnum
	putLE64(content, 184, headEntry.realtime)                           // HeadEntryRealtime
	putLE64(content, 192, tailEntry.realtime)                           // TailEntryRealtime
	putLE64(content, 216, uint64(len(fieldNames)))                      // NFields (remaining[208])

	// Set state if archived
	if state != 0 {
		content[16] = state // state byte at offset 16 in header
	}

	os.WriteFile(path, content, 0644)
}

// buildFieldHashTable builds a FIELD_HASH_TABLE payload (hash items only, no object header).
// It also patches next_hash_offset in field objects within content to handle collisions.
func buildFieldHashTable(fieldNames []string, fileID [16]byte, fieldObjOffsets map[string]uint64, content []byte) ([]byte, uint64) {
	if len(fieldNames) == 0 {
		return nil, 0
	}

	// Use next power of 2 >= len(fieldNames) for table size
	numSlots := uint64(1)
	for numSlots < uint64(len(fieldNames)) {
		numSlots *= 2
	}

	// Each slot is 16 bytes: head_hash_offset (8) + tail_hash_offset (8)
	tableSize := numSlots * 16
	table := make([]byte, tableSize)

	for _, name := range fieldNames {
		hash := journal.SipHash24(fileID, []byte(name))
		slot := hash % numSlots
		off := slot * 16

		foff := fieldObjOffsets[name]

		if binary.LittleEndian.Uint64(table[off:off+8]) == 0 {
			// Empty slot, set head and tail
			putLE64(table, int(off), foff)
			putLE64(table, int(off+8), foff)
		} else {
			// Collision: append to chain by updating tail field object's next_hash_offset
			tailOff := binary.LittleEndian.Uint64(table[off+8 : off+16])
			// Update tail's next_hash_offset to point to new field object
			// next_hash_offset is at byte 24 in field object (after objectHeader=16 + hash=8)
			nextHashFieldOff := int(tailOff) + 24
			putLE64(content, nextHashFieldOff, foff)
			putLE64(table, int(off+8), foff)
		}
	}

	return table, tableSize
}

func main() {
	dir := "testdata"
	os.MkdirAll(dir, 0755)
	writeMinimal(dir)
	writeOneEntry(dir)

	multiDir := filepath.Join(dir, "multi")
	os.MkdirAll(multiDir, 0755)
	writeMultiFile(multiDir)

	fmt.Println("Test data generated successfully")
}
