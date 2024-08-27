// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lru_test

import (
	"bytes"
	cryptorand "crypto/rand"
	"fmt"
	"log"
	mathrand "math/rand"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/gopls/internal/util/lru"
)

func TestCache(t *testing.T) {
	type get struct {
		key  string
		want string
	}
	type set struct {
		key, value string
	}

	tests := []struct {
		label string
		steps []any
	}{
		{"empty cache", []any{
			get{"a", ""},
			get{"b", ""},
		}},
		{"zero-length string", []any{
			set{"a", ""},
			get{"a", ""},
		}},
		{"under capacity", []any{
			set{"a", "123"},
			set{"b", "456"},
			get{"a", "123"},
			get{"b", "456"},
		}},
		{"over capacity", []any{
			set{"a", "123"},
			set{"b", "456"},
			set{"c", "78901"},
			get{"a", ""},
			get{"b", "456"},
			get{"c", "78901"},
		}},
		{"access ordering", []any{
			set{"a", "123"},
			set{"b", "456"},
			get{"a", "123"},
			set{"c", "78901"},
			get{"a", "123"},
			get{"b", ""},
			get{"c", "78901"},
		}},
	}

	for _, test := range tests {
		t.Run(test.label, func(t *testing.T) {
			c := lru.New[string, string](10)
			for i, step := range test.steps {
				switch step := step.(type) {
				case get:
					if got, _ := c.Get(step.key); got != step.want {
						t.Errorf("#%d: c.Get(%q) = %q, want %q", i, step.key, got, step.want)
					}
				case set:
					c.Set(step.key, step.value, len(step.value))
				}
			}
		})
	}
}

// TestConcurrency exercises concurrent access to the same entry.
//
// It is a copy of TestConcurrency from the filecache package.
func TestConcurrency(t *testing.T) {
	key := uniqueKey()
	const N = 100 // concurrency level

	// Construct N distinct values, each larger
	// than a typical 4KB OS file buffer page.
	var values [N][8192]byte
	for i := range values {
		if _, err := mathrand.Read(values[i][:]); err != nil {
			t.Fatalf("rand: %v", err)
		}
	}

	cache := lru.New[[32]byte, []byte](100 * 1e6) // 100MB cache

	// get calls Get and verifies that the cache entry
	// matches one of the values passed to Set.
	get := func(mustBeFound bool) error {
		got, ok := cache.Get(key)
		if !ok {
			if !mustBeFound {
				return nil
			}
			return fmt.Errorf("Get did not return a value")
		}
		for _, want := range values {
			if bytes.Equal(want[:], got) {
				return nil // a match
			}
		}
		return fmt.Errorf("Get returned a value that was never Set")
	}

	// Perform N concurrent calls to Set and Get.
	// All sets must succeed.
	// All gets must return nothing, or one of the Set values;
	// there is no third possibility.
	var group errgroup.Group
	for i := range values {
		i := i
		v := values[i][:]
		group.Go(func() error {
			cache.Set(key, v, len(v))
			return nil
		})
		group.Go(func() error { return get(false) })
	}
	if err := group.Wait(); err != nil {
		if strings.Contains(err.Error(), "operation not supported") ||
			strings.Contains(err.Error(), "not implemented") {
			t.Skipf("skipping: %v", err)
		}
		t.Fatal(err)
	}

	// A final Get must report one of the values that was Set.
	if err := get(true); err != nil {
		t.Fatalf("final Get failed: %v", err)
	}
}

// uniqueKey returns a key that has never been used before.
func uniqueKey() (key [32]byte) {
	if _, err := cryptorand.Read(key[:]); err != nil {
		log.Fatalf("rand: %v", err)
	}
	return
}
