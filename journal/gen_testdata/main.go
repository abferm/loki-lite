//go:build ignore

package main

import (
	"encoding/binary"
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

func main() {
	dir := "testdata"
	os.MkdirAll(dir, 0755)
	writeMinimal(dir)
	writeOneEntry(dir)
}
