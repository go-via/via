package core

import "strings"

// allowedAvatarTypes are the raster image media types that are safe to store
// and serve inline. SVG is deliberately excluded: it can carry <script>, so
// serving it inline at /avatar/{id} would be a stored-XSS vector.
var allowedAvatarTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// IsAllowedAvatarType reports whether contentType (e.g. the result of
// http.DetectContentType) names an image type safe to serve inline. Media-type
// parameters are ignored and the match is case-insensitive.
func IsAllowedAvatarType(contentType string) bool {
	media, _, _ := strings.Cut(contentType, ";")
	return allowedAvatarTypes[strings.ToLower(strings.TrimSpace(media))]
}
