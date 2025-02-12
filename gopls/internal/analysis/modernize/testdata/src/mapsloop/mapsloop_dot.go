//go:build go1.23

package mapsloop

import . "maps"

var _ = Clone[M] // force "maps" import so that each diagnostic doesn't add one

func useCopyDot(dst, src map[int]string) {
	// Replace loop by maps.Copy.
	for key, value := range src {
		dst[key] = value // want "Replace m\\[k\\]=v loop with maps.Copy"
	}
}

func useCloneDot(src map[int]string) {
	// Replace make(...) by maps.Clone.
	dst := make(map[int]string, len(src))
	for key, value := range src {
		dst[key] = value // want "Replace m\\[k\\]=v loop with maps.Clone"
	}
	println(dst)
}
