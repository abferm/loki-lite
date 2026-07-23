package journal

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	journalMagic = "LPKSHHRH"

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

type Reader struct {
	src    io.ReadSeeker
	size   uint64
	closer io.Closer
	header FileHeader
	entries []Entry
	pos    int
}

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

func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open journal file: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to stat journal file: %w", err)
	}

	r := &Reader{src: f, size: uint64(info.Size()), closer: f}
	if err := r.readHeader(); err != nil {
		f.Close()
		return nil, err
	}

	if err := r.readEntries(); err != nil {
		f.Close()
		return nil, err
	}

	return r, nil
}

func OpenDirectory(dir string) (*Reader, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk journal directory: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no journal files found in %s", dir)
	}

	sort.Strings(files)

	return Open(files[0])
}

func (r *Reader) readHeader() error {
	if _, err := io.ReadFull(r.src, r.header.Signature[:]); err != nil {
		return fmt.Errorf("failed to read signature: %w", err)
	}

	if string(r.header.Signature[:]) != journalMagic {
		return fmt.Errorf("invalid journal magic: %q", string(r.header.Signature[:]))
	}

	var remaining [headerSize - 8]byte
	if _, err := io.ReadFull(r.src, remaining[:]); err != nil {
		return fmt.Errorf("failed to read header: %w", err)
	}

	r.header.CompatibleFlags = binary.LittleEndian.Uint32(remaining[0:4])
	r.header.IncompatibleFlags = binary.LittleEndian.Uint32(remaining[4:8])
	r.header.State = remaining[8]
	copy(r.header.Reserved[:], remaining[9:16])
	copy(r.header.FileID[:], remaining[16:32])
	copy(r.header.MachineID[:], remaining[32:48])
	copy(r.header.TailEntryBootID[:], remaining[48:64])
	copy(r.header.SeqnumID[:], remaining[64:80])
	r.header.HeaderSize = binary.LittleEndian.Uint64(remaining[80:88])
	r.header.ArenaSize = binary.LittleEndian.Uint64(remaining[88:96])
	r.header.DataTableOffset = binary.LittleEndian.Uint64(remaining[96:104])
	r.header.DataTableSize = binary.LittleEndian.Uint64(remaining[104:112])
	r.header.FieldTableOffset = binary.LittleEndian.Uint64(remaining[112:120])
	r.header.FieldTableSize = binary.LittleEndian.Uint64(remaining[120:128])
	r.header.TailObjectOffset = binary.LittleEndian.Uint64(remaining[128:136])
	r.header.NObjects = binary.LittleEndian.Uint64(remaining[136:144])
	r.header.NEntries = binary.LittleEndian.Uint64(remaining[144:152])
	r.header.TailEntrySeqnum = binary.LittleEndian.Uint64(remaining[152:160])
	r.header.HeadEntrySeqnum = binary.LittleEndian.Uint64(remaining[160:168])
	r.header.EntryArrayOffset = binary.LittleEndian.Uint64(remaining[168:176])
	r.header.HeadEntryRealtime = binary.LittleEndian.Uint64(remaining[176:184])
	r.header.TailEntryRealtime = binary.LittleEndian.Uint64(remaining[184:192])
	r.header.TailEntryMonotonic = binary.LittleEndian.Uint64(remaining[192:200])

	if r.header.HeaderSize > 200 {
		r.header.NData = binary.LittleEndian.Uint64(remaining[200:208])
		r.header.NFields = binary.LittleEndian.Uint64(remaining[208:216])
	}
	if r.header.HeaderSize > 216 {
		r.header.NTags = binary.LittleEndian.Uint64(remaining[216:224])
		r.header.NEntryArrays = binary.LittleEndian.Uint64(remaining[224:232])
	}

	if r.header.HeaderSize > 232 {
		var extra [32]byte
		n, err := io.ReadFull(r.src, extra[:])
		if err != nil && n == 0 {
			return fmt.Errorf("failed to read extended header: %w", err)
		}

		if n >= 8 {
			r.header.DataHashChainDepth = binary.LittleEndian.Uint64(extra[0:8])
		}
		if n >= 16 {
			r.header.FieldHashChainDepth = binary.LittleEndian.Uint64(extra[8:16])
		}
		if n >= 24 {
			r.header.TailEntryArrayOffset = binary.LittleEndian.Uint32(extra[16:20])
			r.header.TailEntryArrayNEnts = binary.LittleEndian.Uint32(extra[20:24])
		}
		if n >= 32 {
			r.header.TailEntryOffset = binary.LittleEndian.Uint64(extra[24:32])
		}
	}

	return nil
}

func (r *Reader) readEntries() error {
	r.entries = nil
	r.pos = 0

	offset := r.header.HeaderSize
	for offset < r.size {
		if _, err := r.src.Seek(int64(offset), io.SeekStart); err != nil {
			return fmt.Errorf("failed to seek to offset %d: %w", offset, err)
		}

		var obj objectHeader
		if err := binary.Read(r.src, binary.LittleEndian, &obj); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to read object header at offset %d: %w", offset, err)
		}

		if obj.Type == objectEntry {
			entry, err := r.readEntry(offset)
			if err != nil {
				return fmt.Errorf("failed to read entry at offset %d: %w", offset, err)
			}
			if entry != nil {
				r.entries = append(r.entries, *entry)
			}
		}

		if obj.Size == 0 {
			break
		}
		offset = (offset + obj.Size + 7) & ^uint64(7)
	}

	return nil
}

func (r *Reader) readEntry(offset uint64) (*Entry, error) {
	if _, err := r.src.Seek(int64(offset), io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek to entry at offset %d: %w", offset, err)
	}

	var obj objectHeader
	if err := binary.Read(r.src, binary.LittleEndian, &obj); err != nil {
		return nil, fmt.Errorf("failed to read entry object header at offset %d: %w", offset, err)
	}

	var entryObj entryObject
	entryObj.ObjectHeader = obj
	var entryFields struct {
		Seqnum   uint64
		Realtime uint64
		Monotonic uint64
		BootID   [16]byte
		XORHash  uint64
	}
	if err := binary.Read(r.src, binary.LittleEndian, &entryFields); err != nil {
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
	}

	isCompact := r.header.IncompatibleFlags&compactFlag != 0

	itemOffset := offset + 64

	for itemOffset < offset+obj.Size {
		if _, err := r.src.Seek(int64(itemOffset), io.SeekStart); err != nil {
			return nil, fmt.Errorf("failed to seek to item at offset %d: %w", itemOffset, err)
		}

		if isCompact {
			var dataOffset uint32
			if err := binary.Read(r.src, binary.LittleEndian, &dataOffset); err != nil {
				return nil, fmt.Errorf("failed to read compact item offset: %w", err)
			}
			itemOffset += 4
			if dataOffset == 0 {
				break
			}
			if err := r.readDataFields(entry, uint64(dataOffset)); err != nil {
				return nil, err
			}
		} else {
			var dataOffset uint64
			if err := binary.Read(r.src, binary.LittleEndian, &dataOffset); err != nil {
				return nil, fmt.Errorf("failed to read item offset: %w", err)
			}
			var hash uint64
			if err := binary.Read(r.src, binary.LittleEndian, &hash); err != nil {
				return nil, fmt.Errorf("failed to read item hash: %w", err)
			}
			itemOffset += 16
			if dataOffset == 0 {
				break
			}
			if err := r.readDataFields(entry, dataOffset); err != nil {
				return nil, err
			}
		}
	}

	return entry, nil
}

func (r *Reader) readDataFields(entry *Entry, dataOffset uint64) error {
	if _, err := r.src.Seek(int64(dataOffset), io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to data object at offset %d: %w", dataOffset, err)
	}

	var obj objectHeader
	if err := binary.Read(r.src, binary.LittleEndian, &obj); err != nil {
		return fmt.Errorf("failed to read data object header: %w", err)
	}

	if obj.Type != objectData {
		return fmt.Errorf("expected data object at offset %d, got type %d", dataOffset, obj.Type)
	}

	isCompact := r.header.IncompatibleFlags&compactFlag != 0
	var dataHeaderSize uint64
	if isCompact {
		dataHeaderSize = 16 + 8 + 8 + 8 + 8 + 8 + 8 + 4 + 4
	} else {
		dataHeaderSize = 16 + 8 + 8 + 8 + 8 + 8 + 8
	}

	payloadSize := obj.Size - dataHeaderSize
	if _, err := r.src.Seek(int64(dataOffset+dataHeaderSize), io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek to data payload: %w", err)
	}
	payload := make([]byte, payloadSize)
	if _, err := io.ReadFull(r.src, payload); err != nil {
		return fmt.Errorf("failed to read data payload: %w", err)
	}

	if equalsIdx := indexOf(payload, '='); equalsIdx >= 0 {
		key := string(payload[:equalsIdx])
		value := string(payload[equalsIdx+1:])
		entry.Fields[key] = value
	}

	return nil
}

func (r *Reader) Signature() string {
	return string(r.header.Signature[:])
}

func (r *Reader) HeaderSize() uint64 {
	return r.header.HeaderSize
}

func (r *Reader) NEntries() uint64 {
	return r.header.NEntries
}

func (r *Reader) NObjects() uint64 {
	return r.header.NObjects
}

func (r *Reader) ArenaSize() uint64 {
	return r.header.ArenaSize
}

func (r *Reader) TailObjectOffset() uint64 {
	return r.header.TailObjectOffset
}

func (r *Reader) Next() bool {
	return r.pos < len(r.entries)
}

func (r *Reader) Entry() *Entry {
	if r.pos >= len(r.entries) {
		return nil
	}
	entry := &r.entries[r.pos]
	r.pos++
	return entry
}

func (r *Reader) Seek(offset int64, whence int) (int64, error) {
	newPos := r.pos
	switch whence {
	case io.SeekStart:
		newPos = int(offset)
	case io.SeekCurrent:
		newPos = r.pos + int(offset)
	case io.SeekEnd:
		newPos = len(r.entries) + int(offset)
	}

	if newPos < 0 {
		newPos = 0
	}
	if newPos > len(r.entries) {
		newPos = len(r.entries)
	}

	r.pos = newPos
	return int64(newPos), nil
}

func (r *Reader) Close() error {
	if r.closer != nil {
		return r.closer.Close()
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