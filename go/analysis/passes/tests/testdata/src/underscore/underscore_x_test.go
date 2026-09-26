// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package underscore_test

import _ "underscore"

func ExampleFoo_Bar() {} // OK because refers to known type Foo_Bar.

func ExampleFoo_Bar_suffix() {} // OK because refers to known type Foo_Bar with valid suffix.

func ExampleFoo_Bar_Method() {} // OK because refers to known method Foo_Bar.Method.

func ExampleFoo_Bar_Method_suffix() {} // OK because refers to known method Foo_Bar.Method with valid suffix.

func ExampleFoo_bar() {} // OK because refers to known type Foo with suffix "bar".

func ExampleFoo_Method() {} // OK because refers to known method Foo.Method.

func ExampleFoo_Do_It() {} // OK because refers to known method Foo.Do_It.

func ExampleFoo_suffix_More() {} // OK because refers to known type Foo with suffix "suffix_More", as in go/doc.

func ExampleOnly_Under() {} // OK because refers to known type Only_Under.

func ExampleAmb_Bar() {} // OK because refers to known type Amb_Bar or method Amb.Bar.

func ExampleAmb_Bar_suffix() {} // OK because refers to known type Amb_Bar or method Amb.Bar with valid suffix.

func ExampleGen_T_Method() {} // OK because refers to known method Gen_T.Method.

func ExampleDo_Thing() {} // OK because refers to known function Do_Thing.

func ExampleDo_Thing_suffix() {} // OK because refers to known function Do_Thing with valid suffix.

func ExampleFoo_Nope() {} // want "ExampleFoo_Nope refers to unknown field or method: Foo.Nope"

func ExampleFoo_Bar_Nope() {} // want "ExampleFoo_Bar_Nope refers to unknown field or method: Foo_Bar.Nope"

func ExampleFoo_Bar_Method_Suffix() {} // want "ExampleFoo_Bar_Method_Suffix has malformed example suffix: Suffix"

func ExampleOnly_Nope() {} // want "ExampleOnly_Nope refers to unknown identifier: Only"
