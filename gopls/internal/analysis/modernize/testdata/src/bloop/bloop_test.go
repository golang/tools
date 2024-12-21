//go:build go1.24

package bloop

import "testing"

func BenchmarkA(b *testing.B) {
	println("slow")
	b.ResetTimer()

	for range b.N { // want "b.N can be modernized using b.Loop.."
	}
}

func BenchmarkB(b *testing.B) {
	// setup
	{
		b.StopTimer()
		println("slow")
		b.StartTimer()
	}

	for i := range b.N { // Nope. Should we change this to "for i := 0; b.Loop(); i++"?
		print(i)
	}

	b.StopTimer()
	println("slow")
}

func BenchmarkC(b *testing.B) {
	// setup
	{
		b.StopTimer()
		println("slow")
		b.StartTimer()
	}

	for i := 0; i < b.N; i++ { // want "b.N can be modernized using b.Loop.."
		println("no uses of i")
	}

	b.StopTimer()
	println("slow")
}

func BenchmarkD(b *testing.B) {
	for i := 0; i < b.N; i++ { // want "b.N can be modernized using b.Loop.."
		println(i)
	}
}

func BenchmarkE(b *testing.B) {
	b.Run("sub", func(b *testing.B) {
		b.StopTimer() // not deleted
		println("slow")
		b.StartTimer() // not deleted

		// ...
	})
	b.ResetTimer()

	for i := 0; i < b.N; i++ { // want "b.N can be modernized using b.Loop.."
		println("no uses of i")
	}

	b.StopTimer()
	println("slow")
}
