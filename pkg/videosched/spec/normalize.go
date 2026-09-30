package spec

import (
	"strconv"
	"strings"
)

// pixelSize parses "WxH" (or "W*H") pixel sizes made of positive integers.
func pixelSize(size string) (width, height int, ok bool) {
	w, h, found := strings.Cut(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(size)), "*", "x"), "x")
	if !found {
		return 0, 0, false
	}
	width, errW := strconv.Atoi(w)
	height, errH := strconv.Atoi(h)
	return width, height, errW == nil && errH == nil && width > 0 && height > 0
}

// shortSideTier maps a pixel size to its short side, e.g. 720x1280 -> 720p.
func shortSideTier(size string) (string, bool) {
	width, height, ok := pixelSize(size)
	if !ok {
		return "", false
	}
	return strconv.Itoa(min(width, height)) + "p", true
}
