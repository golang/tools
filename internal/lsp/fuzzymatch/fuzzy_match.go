// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// fuzzymatch implements fuzzy string matching suitable for matching a user's
// query against Go identifiers. See Match documentation for more details.
package fuzzymatch

import (
	"unicode"
	"unicode/utf8"
)

const (
	// fuzzyStartMatch is a bonus for matching the first rune of an identifier.
	// This includes "b" against "bar" and "foo.bar".
	fuzzyStartMatch = 2

	// fuzzyWordMatch is a bonus for matching the first rune of a non-initial
	// word. This includes "b" against "foo_bar" and "fooBar".
	fuzzyWordMatch = 1

	// fuzzyConsecMatch is a bonus for each consecutively matched rune after the
	// first. This includes "ba" against "fooBar".
	fuzzyConsecMatch = 1

	// fuzzyPrefixMatch is like fuzzyConsecMatch but only for matches anchored at
	// the start of an identifier. This includes "ba" against "bar".
	fuzzyPrefixMatch = 1
)

// Match matches the user input against a target string. It returns
// whether the target matches the input string, and if so, a score (higher
// meaning a better match). See the above constants for more detail on
// the scoring.
//
// In order to match, each input rune must appear in order in the target string.
// Uppercase input runes must match exactly with target runes, but lowercase
// input runes match case insensitively. Matching is greedy, so the first target
// rune that matches an input rune is always taken.
func Match(input, target string) (bool, int) {
	if len(input) == 0 {
		return true, 0
	}

	if len(input) > len(target) {
		return false, 0
	}

	var (
		score               int
		prevTargetRune      rune
		previousRuneMatched bool // did the previous target rune match an input rune
		prefixStreak        bool // do we match consecutively from start of identifier
		inputRune           rune // current rune from input string
	)

	for targetByteIdx, targetRune := range target {
		// If this is the first iteration or if previous iteration matched, advance
		// to the next rune in the input string.
		if targetByteIdx == 0 || previousRuneMatched {
			var inputRuneSize int
			inputRune, inputRuneSize = utf8.DecodeRuneInString(input)
			if inputRune == utf8.RuneError {
				return false, 0
			}
			input = input[inputRuneSize:]
		}

		// startOfIdentifier is true if we are at the first rune of target, or
		// the first rune after a period.
		startOfIdentifier := targetByteIdx == 0 || prevTargetRune == '.'

		// At the start of an identifier we begin a prefix streak.
		if startOfIdentifier {
			prefixStreak = true
		}

		var match bool
		// Uppercase input runes must match exactly. This allows somewhat for
		// overriding a greedy lowercase match by using uppercase input. For
		// example, searching "abar" against "abcBart" will match "<ab>cB<ar>t",
		// but "aBar" will match "<a>bc<Bar>t".
		if unicode.IsUpper(inputRune) {
			match = inputRune == targetRune
		} else {
			match = runesEqualFold(inputRune, targetRune)
		}

		if match {
			// Matches the start of an identifer.
			if startOfIdentifier {
				score += fuzzyStartMatch
			}

			// Check if we match the start of a word within an identifer.
			if targetByteIdx > 0 {
				switch {
				case prevTargetRune == '_' && !previousRuneMatched:
					// Matches the start of a word starting after an underscore.
					score += fuzzyWordMatch
				case unicode.IsUpper(targetRune) && unicode.IsLower(prevTargetRune):
					// Matches the start of camel case word.
					score += fuzzyWordMatch
				}
			}

			// Consecutive match.
			if previousRuneMatched {
				score += fuzzyConsecMatch
				// Consecutive match from the start of an identifier.
				if prefixStreak {
					score += fuzzyPrefixMatch
				}
			}

			previousRuneMatched = true

			if len(input) == 0 {
				return true, score
			}
		} else {
			previousRuneMatched = false
			prefixStreak = false
		}

		prevTargetRune = targetRune
	}

	return false, 0
}

// runesEqualFold returns whether tr and sr are equivalent taking into
// account unicode case folding.
func runesEqualFold(tr, sr rune) bool {
	// Adapted directly from the loop in strings.EqualFold().

	// Easy case.
	if tr == sr {
		return true
	}

	// Make sr < tr to simplify what follows.
	if tr < sr {
		tr, sr = sr, tr
	}
	// Fast check for ASCII.
	if tr < utf8.RuneSelf {
		// ASCII only, sr/tr must be upper/lower case
		if 'A' <= sr && sr <= 'Z' && tr == sr+'a'-'A' {
			return true
		}
		return false
	}

	// General case. SimpleFold(x) returns the next equivalent rune > x
	// or wraps around to smaller values.
	r := unicode.SimpleFold(sr)
	for r != sr && r < tr {
		r = unicode.SimpleFold(r)
	}
	return r == tr
}
