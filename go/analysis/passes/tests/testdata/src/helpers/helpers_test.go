package helpers_test

import "testing"

func helperOK(t *testing.T, s string) {
	t.Helper()
}

func helperUnnamed(*testing.T) {} // want "unnamed test helper parameter"

func helperUnderscore(_ *testing.T) {} // want "unnamed test helper parameter"

func helperT(t *testing.T) {} // want "first statement of test helper must be t.Helper()"

func helperB(s string, b *testing.B) { // want "first statement of test helper must be b.Helper()"
	b.Fail()
}

func helperF(f *testing.F, i int) { // want "first statement of test helper must be f.Helper()"
	panic(i)
}

func helperTB(t testing.TB) {} // want "first statement of test helper must be t.Helper()"
