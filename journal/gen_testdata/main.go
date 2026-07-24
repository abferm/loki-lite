//go:build ignore

package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
)

const journalMagic = "LPKSHHRH"
const headerSize = 272

const (
	objectData  = 1
	objectEntry = 3
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

type dataDef struct {
	key   string
	value string
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

	writeJournalFile(filepath.Join(dir, "system.journal"), entries1, 1, 2)
	writeJournalFile(filepath.Join(dir, "system@0000000000000002-0000000000000001.journal"), entries2, 3, 4)
}

func writeJournalFile(path string, entries []entryDef, headSeqnum, tailSeqnum uint64) {
	var content []byte

	// Build all entries
	type entryInfo struct {
		offset uint64
		size   uint64
	}
	var entryInfos []entryInfo

	// We'll build the file content after we know all offsets
	// First pass: calculate sizes
	const dataObjFields = 48
	totalDataSize := uint64(0)
	for _, e := range entries {
		for key, val := range e.fields {
			payload := []byte(key + "=" + val)
			dSize := uint64(16) + dataObjFields + uint64(len(payload))
			dSize = (dSize + 7) & ^uint64(7) // align to 8
			totalDataSize += dSize
		}
	}

	totalEntrySize := uint64(0)
	for _, e := range entries {
		entryItemsSize := uint64(len(e.fields) * 16)
		entryObjSize := uint64(64) + entryItemsSize
		totalEntrySize += entryObjSize
	}

	// Build file: header + entries + data
	fileSize := uint64(headerSize) + totalEntrySize + totalDataSize
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

			fieldIdx++
		}

		entryInfos = append(entryInfos, entryInfo{offset: entryOff, size: entryObjSize})
	}

	// Fix up header fields
	headEntry := entries[0]
	tailEntry := entries[len(entries)-1]
	lastEntryOff := entryInfos[len(entryInfos)-1].offset

	// Set header fields (file offsets, not remaining offsets)
	// remaining[0] = file[8], so file_offset = remaining_offset + 8
	putLE64(content, 88, headerSize)                                    // HeaderSize (remaining[80])
	putLE64(content, 136, lastEntryOff)                                 // TailObjectOffset (remaining[128])
	putLE64(content, 144, uint64(len(entries)))                         // NObjects (remaining[136])
	putLE64(content, 152, uint64(len(entries)))                         // NEntries (remaining[144])
	putLE64(content, 160, tailEntry.seqnum)                             // TailEntrySeqnum (remaining[152])
	putLE64(content, 168, headEntry.seqnum)                             // HeadEntrySeqnum (remaining[160])
	putLE64(content, 184, headEntry.realtime)                           // HeadEntryRealtime (remaining[176])
	putLE64(content, 192, tailEntry.realtime)                           // TailEntryRealtime (remaining[184])
	putLE64(content, 216, uint64(len(entries)))                         // NFields (remaining[208])

	os.WriteFile(path, content, 0644)
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
