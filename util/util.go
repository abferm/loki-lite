// Package util provides generic helpers shared across the project.
package util

// Unique deduplicates a slice while preserving order.
func Unique[T comparable](in []T) []T {
	seen := make(map[T]struct{}, len(in))
	var out []T
	for _, v := range in {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}
