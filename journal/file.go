package journal

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	journalMagic = "LPKSHHRH"

	stateOffline  = 0
	stateOnline   = 1
	stateArchived = 2

	objectUnused     = 0
	objectData       = 1
	objectField      = 2
	objectEntry      = 3
	objectHashTable  = 4
	objectFieldTable = 5
	objectEntryArray = 6
	objectTag        = 7

	headerSize = 240

	compactFlag = 1 << 4
)

// File reads a single journald journal file. It holds one entry at a time in
// memory (streaming iteration, not eager load). For multi-file journals, use
// Journal instead — File is strictly single-file.
//
// File is not safe for concurrent use. Each goroutine should open its own.
type File struct {
	src    io.ReadSeeker
	size   uint64
	closer io.Closer
	header FileHeader
	offset uint64
	entry  *Entry
}

// FileHeader holds the binary header of a journald journal file. HeadEntrySeqnum
// is the oldest (smallest) sequence number in the file; TailEntrySeqnum is the
// newest (largest). Head/TailRealtime are the corresponding wall-clock timestamps
// as microseconds since the Unix epoch. State indicates file lifecycle: 0=offline,
// 1=online (active), 2=archived (rotated).
type FileHeader struct {
	Signature            [8]byte
	CompatibleFlags      uint32
	IncompatibleFlags    uint32
	State                uint8
	Reserved             [7]byte
	FileID               [16]byte
	MachineID            [16]byte
	TailEntryBootID      [16]byte
	SeqnumID             [16]byte
	HeaderSize           uint64
	ArenaSize            uint64
	DataTableOffset      uint64
	DataTableSize        uint64
	FieldTableOffset     uint64
	FieldTableSize       uint64
	TailObjectOffset     uint64
	NObjects             uint64
	NEntries             uint64
	TailEntrySeqnum      uint64
	HeadEntrySeqnum      uint64
	EntryArrayOffset     uint64
	HeadEntryRealtime    uint64
	TailEntryRealtime    uint64
	TailEntryMonotonic   uint64
	NData                uint64
	NFields              uint64
	NTags                uint64
	NEntryArrays         uint64
	DataHashChainDepth   uint64
	FieldHashChainDepth  uint64
	TailEntryArrayOffset uint32
	TailEntryArrayNEnts  uint32
	TailEntryOffset      uint64
}

type objectHeader struct {
	Type     uint8
	Flags    uint8
	Reserved [6]byte
	Size     uint64
}

type entryObject struct {
	ObjectHeader objectHeader
	Seqnum       uint64
	Realtime     uint64
	Monotonic    uint64
	BootID       [16]byte
	XORHash      uint64
}

// Open opens a journald journal file for reading. Returns error if the file
// doesn't exist, isn't a valid journal file, or can't be read. The File
// starts positioned at the first entry (after the header).
func Open(path string) (*File, error) {
	src, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open journal file: %w", err)
	}

	info, err := src.Stat()
	if err != nil {
		src.Close()
		return nil, fmt.Errorf("failed to stat journal file: %w", err)
	}

	f := &File{src: src, size: uint64(info.Size()), closer: src}
	if err := f.readHeader(); err != nil {
		src.Close()
		return nil, err
	}

	f.offset = f.header.HeaderSize

	return f, nil
}

func (f *File) readHeader() error {
	if _, err := io.ReadFull(f.src, f.header.Signature[:]); err != nil {
		return fmt.Errorf("failed to read signature: %w", err)
	}

	if string(f.header.Signature[:]) != journalMagic {
		return fmt.Errorf("invalid journal magic: %q", string(f.header.Signature[:]))
	}

	var remaining [headerSize - 8]byte
	if _, err := io.ReadFull(f.src, remaining[:]); err != nil {
		return fmt.Errorf("failed to read header: %w", err)
	}

	f.header.CompatibleFlags = binary.LittleEndian.Uint32(remaining[0:4])
	f.header.IncompatibleFlags = binary.LittleEndian.Uint32(remaining[4:8])
	f.header.State = remaining[8]
	copy(f.header.Reserved[:], remaining[9:16])
	copy(f.header.FileID[:], remaining[16:32])
	copy(f.header.MachineID[:], remaining[32:48])
	copy(f.header.TailEntryBootID[:], remaining[48:64])
	copy(f.header.SeqnumID[:], remaining[64:80])
	f.header.HeaderSize = binary.LittleEndian.Uint64(remaining[80:88])
	f.header.ArenaSize = binary.LittleEndian.Uint64(remaining[88:96])
	f.header.DataTableOffset = binary.LittleEndian.Uint64(remaining[96:104])
	f.header.DataTableSize = binary.LittleEndian.Uint64(remaining[104:112])
	f.header.FieldTableOffset = binary.LittleEndian.Uint64(remaining[112:120])
	f.header.FieldTableSize = binary.LittleEndian.Uint64(remaining[120:128])
	f.header.TailObjectOffset = binary.LittleEndian.Uint64(remaining[128:136])
	f.header.NObjects = binary.LittleEndian.Uint64(remaining[136:144])
	f.header.NEntries = binary.LittleEndian.Uint64(remaining[144:152])
	f.header.TailEntrySeqnum = binary.LittleEndian.Uint64(remaining[152:160])
	f.header.HeadEntrySeqnum = binary.LittleEndian.Uint64(remaining[160:168])
	f.header.EntryArrayOffset = binary.LittleEndian.Uint64(remaining[168:176])
	f.header.HeadEntryRealtime = binary.LittleEndian.Uint64(remaining[176:184])
	f.header.TailEntryRealtime = binary.LittleEndian.Uint64(remaining[184:192])
	f.header.TailEntryMonotonic = binary.LittleEndian.Uint64(remaining[192:200])

	if f.header.HeaderSize > 200 {
		f.header.NData = binary.LittleEndian.Uint64(remaining[200:208])
		f.header.NFields = binary.LittleEndian.Uint64(remaining[208:216])
	}
	if f.header.HeaderSize > 216 {
		f.header.NTags = binary.LittleEndian.Uint64(remaining[216:224])
		f.header.NEntryArrays = binary.LittleEndian.Uint64(remaining[224:232])
	}

	if f.header.HeaderSize > 232 {
		var extra [32]byte
		n, err := io.ReadFull(f.src, extra[:])
		if err != nil && n == 0 {
			return fmt.Errorf("failed to read extended header: %w", err)
		}

		if n >= 8 {
			f.header.DataHashChainDepth = binary.LittleEndian.Uint64(extra[0:8])
		}
		if n >= 16 {
			f.header.FieldHashChainDepth = binary.LittleEndian.Uint64(extra[8:16])
		}
		if n >= 24 {
			f.header.TailEntryArrayOffset = binary.LittleEndian.Uint32(extra[16:20])
			f.header.TailEntryArrayNEnts = binary.LittleEndian.Uint32(extra[20:24])
		}
		if n >= 32 {
			f.header.TailEntryOffset = binary.LittleEndian.Uint64(extra[24:32])
		}
	}

	return nil
}

// ReloadHeader re-reads the file header from disk and updates the cached size.
// Use after the active journal file may have grown or been rotated to an archived
// state. Calls readHeader internally, so header fields (HeadEntrySeqnum,
// TailEntrySeqnum, State, etc.) are refreshed on success.
func (f *File) ReloadHeader() error {
	if osf, ok := f.src.(*os.File); ok {
		if info, err := osf.Stat(); err == nil {
			f.size = uint64(info.Size())
		}
	}

	if _, err := f.src.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to header: %w", err)
	}

	return f.readHeader()
}

// State returns the file's lifecycle state from its header: 0=offline,
// 1=online (active, receiving new entries), 2=archived (rotated, no new entries).
func (f *File) State() uint8 {
	return f.header.State
}

func (f *File) readEntry(offset uint64) (*Entry, error) {
	if _, err := f.src.Seek(int64(offset), io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek to entry at offset %d: %w", offset, err)
	}

	var obj objectHeader
	if err := binary.Read(f.src, binary.LittleEndian, &obj); err != nil {
		return nil, fmt.Errorf("failed to read entry object header at offset %d: %w", offset, err)
	}

	var entryObj entryObject
	entryObj.ObjectHeader = obj
	var entryFields struct {
		Seqnum    uint64
		Realtime  uint64
		Monotonic uint64
		BootID    [16]byte
		XORHash   uint64
	}
	if err := binary.Read(f.src, binary.LittleEndian, &entryFields); err != nil {
		return nil, fmt.Errorf("failed to read entry object at offset %d: %w", offset, err)
	}
	entryObj.Seqnum = entryFields.Seqnum
	entryObj.Realtime = entryFields.Realtime
	entryObj.Monotonic = entryFields.Monotonic
	entryObj.BootID = entryFields.BootID
	entryObj.XORHash = entryFields.XORHash

	entry := &Entry{
		Timestamp: time.Unix(int64(entryObj.Realtime/1000000), int64(entryObj.Realtime%1000000)*1000),
		Fields:    make(map[string]string),
		obj:       entryObj,
	}

	isCompact := f.header.IncompatibleFlags&compactFlag != 0

	itemOffset := offset + 64

	for itemOffset < offset+obj.Size {
		if _, err := f.src.Seek(int64(itemOffset), io.SeekStart); err != nil {
			return nil, fmt.Errorf("failed to seek to item at offset %d: %w", itemOffset, err)
		}

		if isCompact {
			var dataOffset uint32
			if err := binary.Read(f.src, binary.LittleEndian, &dataOffset); err != nil {
				return nil, fmt.Errorf("failed to read compact item offset: %w", err)
			}
			itemOffset += 4
			if dataOffset == 0 {
				break
			}
			if err := f.readDataFields(entry, uint64(dataOffset)); err != nil {
				return nil, err
			}
		} else {
			var dataOffset uint64
			if err := binary.Read(f.src, binary.LittleEndian, &dataOffset); err != nil {
				return nil, fmt.Errorf("failed to read item offset: %w", err)
			}
			var hash uint64
			if err := binary.Read(f.src, binary.LittleEndian, &hash); err != nil {
				return nil, fmt.Errorf("failed to read item hash: %w", err)
			}
			itemOffset += 16
			if dataOffset == 0 {
				break
			}
			if err := f.readDataFields(entry, dataOffset); err != nil {
				return nil, err
			}
		}
	}

	return entry, nil
}

func (f *File) readDataFields(entry *Entry, dataOffset uint64) error {
	if _, err := f.src.Seek(int64(dataOffset), io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to data object at offset %d: %w", dataOffset, err)
	}

	var obj objectHeader
	if err := binary.Read(f.src, binary.LittleEndian, &obj); err != nil {
		return fmt.Errorf("failed to read data object header: %w", err)
	}

	if obj.Type != objectData {
		return fmt.Errorf("expected data object at offset %d, got type %d", dataOffset, obj.Type)
	}

	isCompact := f.header.IncompatibleFlags&compactFlag != 0
	var dataHeaderSize uint64
	if isCompact {
		dataHeaderSize = 16 + 8 + 8 + 8 + 8 + 8 + 8 + 4 + 4
	} else {
		dataHeaderSize = 16 + 8 + 8 + 8 + 8 + 8 + 8
	}

	payloadSize := obj.Size - dataHeaderSize
	if _, err := f.src.Seek(int64(dataOffset+dataHeaderSize), io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to data payload: %w", err)
	}
	payload := make([]byte, payloadSize)
	if _, err := io.ReadFull(f.src, payload); err != nil {
		return fmt.Errorf("failed to read data payload: %w", err)
	}

	if equalsIdx := indexOf(payload, '='); equalsIdx >= 0 {
		key := string(payload[:equalsIdx])
		value := string(payload[equalsIdx+1:])
		entry.Fields[key] = value
	}

	return nil
}

// Signature returns the 8-byte magic string that identifies valid journald files.
func (f *File) Signature() string {
	return string(f.header.Signature[:])
}

// HeaderSize returns the byte size of the file header. Entries begin at this offset.
func (f *File) HeaderSize() uint64 {
	return f.header.HeaderSize
}

// NEntries returns the total number of log entries recorded in this file's header.
// This is the count at file open time — use Next() to iterate and count actual
// entries reachable from the current offset.
func (f *File) NEntries() uint64 {
	return f.header.NEntries
}

// HeadEntrySeqnum returns the smallest sequence number in this file (the oldest entry).
// Files are sorted by this value in Journal. Used for seqnum-based file lookup.
func (f *File) HeadEntrySeqnum() uint64 {
	return f.header.HeadEntrySeqnum
}

// TailEntrySeqnum returns the largest sequence number in this file (the newest entry).
// Used for rotation detection and gap detection in Journal.Next().
func (f *File) TailEntrySeqnum() uint64 {
	return f.header.TailEntrySeqnum
}

// HeadEntryRealtime returns the wall-clock timestamp (microseconds since Unix epoch)
// of the oldest entry in this file. Used by Journal.SeekRealtime to pick the best file.
func (f *File) HeadEntryRealtime() uint64 {
	return f.header.HeadEntryRealtime
}

// TailEntryRealtime returns the wall-clock timestamp (microseconds since Unix epoch)
// of the newest entry in this file. Used by Journal.SeekRealtime to skip files
// entirely before the target time.
func (f *File) TailEntryRealtime() uint64 {
	return f.header.TailEntryRealtime
}

// NObjects returns the total number of objects (entries, data fields, hash tables,
// etc.) in the file's arena.
func (f *File) NObjects() uint64 {
	return f.header.NObjects
}

// ArenaSize returns the total byte size of the data arena (all objects after the header).
func (f *File) ArenaSize() uint64 {
	return f.header.ArenaSize
}

// TailObjectOffset returns the byte offset of the tail object (most recently written).
func (f *File) TailObjectOffset() uint64 {
	return f.header.TailObjectOffset
}

// Path returns the filesystem path of the underlying file, or empty string if the
// File was constructed from an in-memory source.
func (f *File) Path() string {
	if f, ok := f.src.(*os.File); ok {
		return f.Name()
	}
	return ""
}

// Exists reports whether the file still exists on disk. Used by Journal to detect
// deleted files during cleanup.
func (f *File) Exists() bool {
	path := f.Path()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// Next advances to the next entry in the file, skipping non-entry objects
// (data fields, hash tables, etc.). Returns true and stores the entry if found.
// Returns false at EOF or on read error. Call after Open or SeekHead/SeekRealtime
// to read the first entry.
func (f *File) Next() bool {
	for f.offset < f.size {
		if _, err := f.src.Seek(int64(f.offset), io.SeekStart); err != nil {
			return false
		}

		var obj objectHeader
		if err := binary.Read(f.src, binary.LittleEndian, &obj); err != nil {
			return false
		}

		if obj.Size == 0 {
			return false
		}

		next := (f.offset + obj.Size + 7) & ^uint64(7)

		if obj.Type == objectEntry {
			entry, err := f.readEntry(f.offset)
			if err != nil {
				return false
			}
			f.offset = next
			f.entry = entry
			return true
		}

		f.offset = next
	}
	return false
}

// Entry returns the current entry, or nil if no entry has been read yet.
func (f *File) Entry() *Entry {
	return f.entry
}

// NextEntry is a convenience wrapper: calls Next() then returns (Entry(), true)
// on success, or (nil, false) if no more entries.
func (f *File) NextEntry() (*Entry, bool) {
	if f.Next() {
		return f.Entry(), true
	}
	return nil, false
}

// SeekHead resets the read position to the first entry in the file. The next
// call to Next() will return the oldest entry.
func (f *File) SeekHead() {
	f.offset = f.header.HeaderSize
	f.entry = nil
}

// Seek positions the reader at the entry with the given seqnum and reads it
// into Entry(). If seqnum < HeadEntrySeqnum, loads the first entry (may be
// after the target). If seqnum >= TailEntrySeqnum, loads the last entry.
// Returns true if the loaded entry's seqnum is >= seqnum.
func (f *File) Seek(seqnum uint64) bool {
	f.SeekHead()
	for f.Next() {
		if f.entry.Seqnum() >= seqnum {
			return true
		}
	}
	return false
}

// SeekTail positions the reader at the last entry in the file and reads it
// into Entry(). Equivalent to Seek(tailSeqnum).
func (f *File) SeekTail() {
	f.Seek(f.header.TailEntrySeqnum)
}

// SeekRealtime positions the reader at the first entry whose realtime timestamp
// is >= t and reads it into Entry(). If t is before the head realtime, loads
// the first entry (may be after t). If t is after the tail realtime, loads the
// last entry. Returns true if the loaded entry's timestamp is >= t.
func (f *File) SeekRealtime(t time.Time) bool {
	usec := uint64(t.UnixMicro())

	f.SeekHead()
	for f.Next() {
		if f.entry.Realtime() >= usec {
			return true
		}
	}
	return false
}

func (f *File) containsSeqnum(seqnum uint64) bool {
	if seqnum == 0 {
		return false
	}
	return seqnum >= f.header.HeadEntrySeqnum && seqnum <= f.header.TailEntrySeqnum
}

// Close releases the underlying file descriptor. After Close, all methods return
// zero values or errors.
func (f *File) Close() error {
	if f.closer != nil {
		return f.closer.Close()
	}
	return nil
}

func indexOf(data []byte, b byte) int {
	for i, v := range data {
		if v == b {
			return i
		}
	}
	return -1
}
