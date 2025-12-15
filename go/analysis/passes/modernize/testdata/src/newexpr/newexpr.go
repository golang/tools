//go:build go1.26

package newexpr

// intVar returns a new var whose value is i.
func intVar(i int) *int { return &i } // want `intVar can be an inlinable wrapper around new\(expr\)` intVar:"newlike"

func int64Var(i int64) *int64 { return &i } // want `int64Var can be an inlinable wrapper around new\(expr\)` int64Var:"newlike"

func stringVar(s string) *string { return &s } // want `stringVar can be an inlinable wrapper around new\(expr\)` stringVar:"newlike"

func varOf[T any](x T) *T { return &x } // want `varOf can be an inlinable wrapper around new\(expr\)` varOf:"newlike"

var (
	s struct {
		int
		string
	}
	_ = intVar(123)       // want `call of intVar\(x\) can be simplified to new\(x\)`
	_ = int64Var(123)     // nope: implicit conversion from untyped int to int64
	_ = stringVar("abc")  // want `call of stringVar\(x\) can be simplified to new\(x\)`
	_ = varOf(s)          // want `call of varOf\(x\) can be simplified to new\(x\)`
	_ = varOf(123)        // want `call of varOf\(x\) can be simplified to new\(x\)`
	_ = varOf(int64(123)) // want `call of varOf\(x\) can be simplified to new\(x\)`
	_ = varOf[int](123)   // want `call of varOf\(x\) can be simplified to new\(x\)`
	_ = varOf[int64](123) // nope: implicit conversion from untyped int to int64
	_ = varOf(            // want `call of varOf\(x\) can be simplified to new\(x\)`
		varOf(123)) // want `call of varOf\(x\) can be simplified to new\(x\)`
)
