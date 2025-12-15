// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package file

import (
	"crypto/sha256"
	"fmt"
)

// A Hash is a cryptographic digest of the contents of a file.
// (Although at 32B it is larger than a 16B string header, it is smaller
// and has better locality than the string header + 64B of hex digits.)
type Hash [sha256.Size]byte

// HashOf returns the hash of some data.
func HashOf(data []byte) Hash {
	return Hash(sha256.Sum256(data))
}

// String returns the digest as a string of hex digits.
func (h Hash) String() string {
	return fmt.Sprintf("%64x", [sha256.Size]byte(h))
}

// XORWith updates *h to *h XOR h2.
func (h *Hash) XORWith(h2 Hash) {
	// Small enough that we don't need crypto/subtle.XORBytes.
	for i := range h {
		h[i] ^= h2[i]
	}
}
