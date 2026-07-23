package journal

import (
	"fmt"
	"time"
)

type Entry struct {
	Timestamp  time.Time
	Fields     map[string]string
	BootID     string
	MessageID  string
	Priority   int
	UID        uint32
	GID        uint32
	PID        uint32
	Transport  string
}

func (e *Entry) Message() string {
	return e.Fields["MESSAGE"]
}

func (e *Entry) SyslogFacility() string {
	return e.Fields["SYSLOG_FACILITY"]
}

func (e *Entry) SyslogIdentifier() string {
	return e.Fields["SYSLOG_IDENTIFIER"]
}

func (e *Entry) Unit() string {
	return e.Fields["_SYSTEMD_UNIT"]
}

func (e *Entry) Hostname() string {
	return e.Fields["_HOSTNAME"]
}

func (e *Entry) Get(key string) string {
	return e.Fields[key]
}

func (e *Entry) String() string {
	return fmt.Sprintf("[%s] %s", e.Timestamp.Format(time.RFC3339), e.Message())
}