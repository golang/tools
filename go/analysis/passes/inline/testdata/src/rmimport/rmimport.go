package rmimport

import "a"

// Test that application of two fixes that each remove the second-last
// import of "a" results in removal of the import. This is implemented
// by the general analysis fix logic, not by any one analyzer.
func _() {
	print(a.T{}.Two()) // want `should be inlined`
	print(a.T{}.Two()) // want `should be inlined`
}
