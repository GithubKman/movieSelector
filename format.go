package main

import (
	"fmt"
	"strings"
)

// DisplayName is the human-friendly "Title (Year)".
func DisplayName(title string, year int) string {
	if year > 0 {
		return fmt.Sprintf("%s (%d)", title, year)
	}
	return title
}

// FolderName follows the naming convention understood by Jellyfin, Plex and
// Emby, e.g. "The Matrix (1999) {tmdb-603}", with characters that are illegal
// on common filesystems removed. Use it as the target directory on the NAS.
func FolderName(title string, year int, tmdbID int) string {
	name := SanitizeFilename(DisplayName(title, year))
	if tmdbID > 0 {
		name += fmt.Sprintf(" {tmdb-%d}", tmdbID)
	}
	return name
}

var filenameReplacer = strings.NewReplacer(
	": ", " - ", ":", "-",
	"/", "-", "\\", "-",
	"<", "", ">", "", "\"", "", "|", "", "?", "", "*", "",
)

func SanitizeFilename(s string) string {
	s = filenameReplacer.Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimRight(s, ". ")
}
