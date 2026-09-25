// Package demos holds the live demos the content pages embed as children. Each
// file is also its own documentation: FS carries the verbatim source the demo
// card's Source tab shows, so what runs on the page and what the reader copies
// can never drift. That embed is why this package can never import demo back.
package demos

import "embed"

// FS is every file of this package, source and all, which is what the Source
// tab of a demo card shows.
//
//go:embed *.go
var FS embed.FS
