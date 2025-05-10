// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mcp

import (
	"iter"

	"golang.org/x/tools/internal/mcp/internal/util"
)

// This file contains implementations that are common to all features.
// A feature is an item provided to a peer. In the 2025-03-26 spec,
// the features are prompt, tool, resource and root.

// A featureSet is a collection of features of type T.
// Every feature has a unique ID, and the spec never mentions
// an ordering for the List calls, so what it calls a "list" is actually a set.
type featureSet[T any] struct {
	uniqueID func(T) string
	features map[string]T
}

// newFeatureSet creates a new featureSet for features of type T.
// The argument function should return the unique ID for a single feature.
func newFeatureSet[T any](uniqueIDFunc func(T) string) *featureSet[T] {
	return &featureSet[T]{
		uniqueID: uniqueIDFunc,
		features: make(map[string]T),
	}
}

// add adds each feature to the set if it is not present,
// or replaces an existing feature.
func (s *featureSet[T]) add(fs ...T) {
	for _, f := range fs {
		s.features[s.uniqueID(f)] = f
	}
}

// remove removes all features with the given uids from the set if present,
// and returns whether any were removed.
// It is not an error to remove a nonexistent feature.
func (s *featureSet[T]) remove(uids ...string) bool {
	changed := false
	for _, uid := range uids {
		if _, ok := s.features[uid]; ok {
			changed = true
			delete(s.features, uid)
		}
	}
	return changed
}

// get returns the feature with the given uid.
// If there is none, it returns zero, false.
func (s *featureSet[T]) get(uid string) (T, bool) {
	t, ok := s.features[uid]
	return t, ok
}

// all returns an iterator over of all the features in the set
// sorted by unique ID.
func (s *featureSet[T]) all() iter.Seq[T] {
	return func(yield func(T) bool) {
		for _, f := range util.Sorted(s.features) {
			if !yield(f) {
				return
			}
		}
	}
}
