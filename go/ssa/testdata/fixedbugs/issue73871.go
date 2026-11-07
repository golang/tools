package issue73871

// Regression test for panic instantiating signature for a call append(x, y...).

func f[T ~[]byte](y T) {
	_ = append([]byte(nil), y...)
}

func _() {
	type B []byte
	f(B(nil)) // must not panic
}
