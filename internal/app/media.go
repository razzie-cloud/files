package app

import (
	"path"
	"strings"
)

// Keep preview types deterministic across hosts and never serve arbitrary HTML
// or other active documents inline. Actual codec support belongs to the browser.
func mediaType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".pdf":
		return "application/pdf"
	case ".md", ".markdown", ".mdown", ".mkd":
		return "text/markdown"
	case ".txt", ".text", ".log", ".csv", ".tsv", ".json", ".jsonl", ".ndjson",
		".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf", ".xml", ".html", ".htm",
		".css", ".js", ".mjs", ".ts", ".tsx", ".jsx", ".go", ".py", ".rs",
		".c", ".h", ".cpp", ".hpp", ".java", ".rb", ".php", ".sh", ".bash",
		".sql", ".nix", ".diff", ".patch", ".srt", ".vtt", ".rst":
		return "text/plain"
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
	case ".mp3":
		return "audio/mpeg"
	case ".m4a", ".m4b":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".wav", ".wave":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".weba":
		return "audio/webm"
	case ".aif", ".aiff":
		return "audio/aiff"
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
		switch strings.ToLower(path.Base(name)) {
		case "readme", "license", "licence", "copying", "authors", "changelog", "makefile", "dockerfile", ".gitignore", ".editorconfig":
			return "text/plain"
		}
		return ""
	}
}

func mediaKind(name string) string {
	switch mediaType(name) {
	case "application/pdf":
		return "pdf"
	case "text/plain", "text/markdown":
		return "document"
	}
	t, _, _ := strings.Cut(mediaType(name), "/")
	return t
}
