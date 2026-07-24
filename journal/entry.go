package journal

import (
	"fmt"
	"time"
)

// Entry represents a single journald log record. Fields contains all key-value
// pairs from the entry (MESSAGE, _SYSTEMD_UNIT, etc.). Timestamp is derived from
// the entry's realtime clock value. Seqnum and Realtime provide raw access to the
// on-disk values for use in cursor-based navigation.
type Entry struct {
	Timestamp time.Time
	Fields    map[string]string
	BootID    string
	MessageID string
	Priority  int
	UID       uint32
	GID       uint32
	PID       uint32
	Transport string
	obj       entryObject
}

// Seqnum returns the entry's monotonic sequence number. Seqnums are unique within
// a journal file and increase with each entry. Use for cursor-based iteration —
// a Reader's containsSeqnum checks whether a seqnum falls within a file's range.
func (e *Entry) Seqnum() uint64 {
	return e.obj.Seqnum
}

// Realtime returns the entry's wall-clock timestamp as microseconds since the
// Unix epoch. Use for SeekRealtime comparisons. For human-readable time, use
// the Timestamp field instead.
func (e *Entry) Realtime() uint64 {
	return e.obj.Realtime
}

// Message returns the MESSAGE field value, or empty string if unset.
func (e *Entry) Message() string {
	return e.Fields["MESSAGE"]
}

// SyslogFacility returns the SYSLOG_FACILITY field value (e.g. "4" for cron).
func (e *Entry) SyslogFacility() string {
	return e.Fields["SYSLOG_FACILITY"]
}

// SyslogIdentifier returns the SYSLOG_IDENTIFIER field value (e.g. "sshd").
func (e *Entry) SyslogIdentifier() string {
	return e.Fields["SYSLOG_IDENTIFIER"]
}

// Unit returns the _SYSTEMD_UNIT field value (e.g. "sshd.service").
func (e *Entry) Unit() string {
	return e.Fields["_SYSTEMD_UNIT"]
}

// Hostname returns the _HOSTNAME field value.
func (e *Entry) Hostname() string {
	return e.Fields["_HOSTNAME"]
}

// Get returns the value for an arbitrary field key. For well-known fields,
// prefer the typed accessors (Message, Unit, etc.) which are self-documenting.
func (e *Entry) Get(key string) string {
	return e.Fields[key]
}

// String returns a human-readable summary: "[RFC3339] message text".
func (e *Entry) String() string {
	return fmt.Sprintf("[%s] %s", e.Timestamp.Format(time.RFC3339), e.Message())
}
