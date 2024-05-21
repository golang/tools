// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package protocol

import (
	"encoding/json"
	"fmt"
)

// InsertReplaceEdit is used instead of TextEdit in CompletionItem
// in editors that support it. These two types are alike in appearance
// but can be differentiated by the presence or absence of
// certain properties. UnmarshalJSON of the sum type tries to
// unmarshal as TextEdit only if unmarshal as InsertReplaceEdit fails.
// However, due to this similarity, unmarshal with the other type
// never fails. This file has a custom JSON unmarshaller for
// InsertReplaceEdit, that fails if the required fields are missing.

// UnmarshalJSON unmarshals InsertReplaceEdit with extra
// checks on the presence of "insert" and "replace" properties.
func (e *InsertReplaceEdit) UnmarshalJSON(data []byte) error {
	var required struct {
		NewText string
		Insert  *Range `json:"insert,omitempty"`
		Replace *Range `json:"replace,omitempty"`
	}

	if err := json.Unmarshal(data, &required); err != nil {
		return err
	}
	if required.Insert == nil && required.Replace == nil {
		return fmt.Errorf("not InsertReplaceEdit")
	}
	e.NewText = required.NewText
	e.Insert = *required.Insert
	e.Replace = *required.Replace
	return nil
}
