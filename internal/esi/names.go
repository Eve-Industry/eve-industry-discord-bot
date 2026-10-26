package esi

import "strings"

// Normalize makes item names comparable the way ESI matches them.
func Normalize(name string) string {
	return strings.ToLower(strings.Join(strings.Fields(name), " "))
}
