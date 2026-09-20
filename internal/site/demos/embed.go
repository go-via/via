// Package demos holds the live demos the content pages embed as children. Each
// file is also its own documentation: FS carries the verbatim source the demo
// card's Source tab shows, so what runs on the page and what the reader copies
// can never drift.
package demos

import "embed"

// FS is every file of this package, embedded verbatim.
//
//go:embed *.go
var FS embed.FS
