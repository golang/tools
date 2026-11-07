package issue73594

// Regression test for sanity-check failure caused by not clearing
// Function.subst after building a body-less instantiated function.

type genericType[T any] struct{}

func (genericType[T]) methodWithoutBody()

func callMethodWithoutBody() {
	msg := &genericType[int]{}
	msg.methodWithoutBody()
}
