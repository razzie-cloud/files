package app

import (
	"path"
	"strings"
)

// Keep preview types deterministic across hosts and never serve arbitrary HTML
// or other active documents inline. Actual codec support belongs to the browser.
func mediaType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".jfif":
		return "image/jpeg"
	case ".png", ".apng":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".svg":
		return "image/svg+xml"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/vnd.microsoft.icon"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".heic":
		return "image/heic"
	case ".heif":
		return "image/heif"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".ogv":
		return "video/ogg"
	case ".mov":
		return "video/quicktime"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	case ".mpeg", ".mpg":
		return "video/mpeg"
	default:
		return ""
	}
}

func mediaKind(name string) string {
	t, _, _ := strings.Cut(mediaType(name), "/")
	return t
}
