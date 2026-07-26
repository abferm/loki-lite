// Package query parses LogQL strings into Filter values that can match
// journald entries. Only a subset of LogQL is supported: stream selectors,
// line filters, and simple label filters.
package query

import "github.com/abferm/loki-lite/journal"

// Filter selects journal entries that match a LogQL query.
type Filter interface {
	Match(entry journal.Entry) bool
}

// Parse converts a LogQL query string into a Filter. Returns an error if the
// query uses unsupported LogQL features.
func Parse(logql string) (Filter, error) {
	panic("unimplemented")
}
