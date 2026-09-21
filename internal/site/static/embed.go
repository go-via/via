// Package static is the embed of the site's assets. It exists only so the FS
// is rooted at the asset directory: an embed pattern cannot reach upwards, so
// the directive has to live beside the files it names.
package static

import "embed"

// FS is every served asset, rooted at this directory. The patterns are spelled
// out rather than `*`, which would embed this file and serve it as an asset.
//
//go:embed brand data fonts vendor *.css *.js
var FS embed.FS
