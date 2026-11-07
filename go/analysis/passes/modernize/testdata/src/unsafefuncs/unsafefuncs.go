package unsafefuncs

import "unsafe"

func _(ptr unsafe.Pointer) unsafe.Pointer {
	return unsafe.Pointer(uintptr(ptr) + 1) // want `pointer \+ integer can be simplified using unsafe.Add`
}

type uP = unsafe.Pointer

func _(ptr uP) uP {
	return uP(uintptr(ptr) + 1) // want `pointer \+ integer can be simplified using unsafe.Add`
}

func _(ptr unsafe.Pointer, n int) unsafe.Pointer {
	return unsafe.Pointer(uintptr(ptr) + uintptr(n)) // want `pointer \+ integer can be simplified using unsafe.Add`
}

func _(ptr *byte, len int) *byte {
	return (*byte)(unsafe.Pointer(uintptr(unsafe.Pointer(ptr)) + uintptr(len))) // want `pointer \+ integer can be simplified using unsafe.Add`
}

type namedUP unsafe.Pointer

func _(ptr namedUP) namedUP {
	return namedUP(uintptr(ptr) + 1) // nope: Add does not accept named unsafe.Pointer types
}
