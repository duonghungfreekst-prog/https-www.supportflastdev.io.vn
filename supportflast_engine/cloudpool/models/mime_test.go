package models

import (
	"testing"
)

func TestResolveMimeType_Video(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"video.mp4", "video/mp4"},
		{"clip.webm", "video/webm"},
		{"movie.mkv", "video/x-matroska"},
		{"film.avi", "video/x-msvideo"},
		{"raw.mov", "video/quicktime"},
		{"stream.wmv", "video/x-ms-wmv"},
		{"flash.flv", "video/x-flv"},
		{"broadcast.ts", "video/mp2t"},
		{"video.m4v", "video/x-m4v"},
		{"mobile.3gp", "video/3gpp"},
		{"open.ogv", "video/ogg"},
		{"UPPERCASE.MP4", "video/mp4"},
		{"nested.version.1.0.mkv", "video/x-matroska"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := ResolveMimeType(tt.filename)
			if got != tt.expected {
				t.Errorf("ResolveMimeType(%q) = %q, expected %q", tt.filename, got, tt.expected)
			}
			if !IsMediaStreamable(got) {
				t.Errorf("IsMediaStreamable(%q) for %q expected true", got, tt.filename)
			}
		})
	}
}

func TestResolveMimeType_Audio(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"song.mp3", "audio/mpeg"},
		{"recording.wav", "audio/wav"},
		{"music.ogg", "audio/ogg"},
		{"lossless.flac", "audio/flac"},
		{"track.aac", "audio/aac"},
		{"audio.m4a", "audio/mp4"},
		{"voice.opus", "audio/opus"},
		{"tune.wma", "audio/x-ms-wma"},
		{"soundtrack.mid", "audio/midi"},
		{"instrumental.midi", "audio/midi"},
		{"AUDIO.FLAC", "audio/flac"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := ResolveMimeType(tt.filename)
			if got != tt.expected {
				t.Errorf("ResolveMimeType(%q) = %q, expected %q", tt.filename, got, tt.expected)
			}
			if !IsMediaStreamable(got) {
				t.Errorf("IsMediaStreamable(%q) for %q expected true", got, tt.filename)
			}
		})
	}
}

func TestResolveMimeType_Image(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"photo.jpg", "image/jpeg"},
		{"photo.jpeg", "image/jpeg"},
		{"logo.png", "image/png"},
		{"animation.gif", "image/gif"},
		{"modern.webp", "image/webp"},
		{"diagram.svg", "image/svg+xml"},
		{"graphic.bmp", "image/bmp"},
		{"favicon.ico", "image/x-icon"},
		{"scan.tiff", "image/tiff"},
		{"scan.tif", "image/tiff"},
		{"nextgen.avif", "image/avif"},
		{"PHOTO.PNG", "image/png"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := ResolveMimeType(tt.filename)
			if got != tt.expected {
				t.Errorf("ResolveMimeType(%q) = %q, expected %q", tt.filename, got, tt.expected)
			}
			if IsMediaStreamable(got) {
				t.Errorf("IsMediaStreamable(%q) for image %q should be false", got, tt.filename)
			}
		})
	}
}

func TestResolveMimeType_Office(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"document.pdf", "application/pdf"},
		{"legacy.doc", "application/msword"},
		{"word.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"legacy.xls", "application/vnd.ms-excel"},
		{"sheet.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"legacy.ppt", "application/vnd.ms-powerpoint"},
		{"presentation.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"open.odt", "application/vnd.oasis.opendocument.text"},
		{"open.ods", "application/vnd.oasis.opendocument.spreadsheet"},
		{"open.odp", "application/vnd.oasis.opendocument.presentation"},
		{"data.csv", "text/csv; charset=utf-8"},
		{"table.tsv", "text/tab-separated-values; charset=utf-8"},
		{"readme.txt", "text/plain; charset=utf-8"},
		{"notes.md", "text/plain; charset=utf-8"},
		{"format.rtf", "application/rtf"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := ResolveMimeType(tt.filename)
			if got != tt.expected {
				t.Errorf("ResolveMimeType(%q) = %q, expected %q", tt.filename, got, tt.expected)
			}
			if IsMediaStreamable(got) {
				t.Errorf("IsMediaStreamable(%q) for office %q should be false", got, tt.filename)
			}
		})
	}
}

func TestResolveMimeType_Archive(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"archive.zip", "application/zip"},
		{"archive.rar", "application/vnd.rar"},
		{"archive.7z", "application/x-7z-compressed"},
		{"archive.tar", "application/x-tar"},
		{"archive.gz", "application/gzip"},
		{"archive.tgz", "application/gzip"},
		{"archive.bz2", "application/x-bzip2"},
		{"archive.xz", "application/x-xz"},
		{"PACKAGE.ZIP", "application/zip"},
		{"backup.2026.09.tar", "application/x-tar"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := ResolveMimeType(tt.filename)
			if got != tt.expected {
				t.Errorf("ResolveMimeType(%q) = %q, expected %q", tt.filename, got, tt.expected)
			}
			if IsMediaStreamable(got) {
				t.Errorf("IsMediaStreamable(%q) for archive %q should be false", got, tt.filename)
			}
		})
	}
}

func TestResolveMimeType_Fallbacks(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"unknown_binary.xyz123", "application/octet-stream"},
		{"no_extension", "application/octet-stream"},
		{"empty.", "application/octet-stream"},
		{"config.json", "application/json"},
		{"index.html", "text/html; charset=utf-8"},
		{"app.js", "application/javascript"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := ResolveMimeType(tt.filename)
			if got != tt.expected {
				t.Errorf("ResolveMimeType(%q) = %q, expected %q", tt.filename, got, tt.expected)
			}
		})
	}
}
