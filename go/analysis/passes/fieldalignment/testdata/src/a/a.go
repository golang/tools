package a

type Good struct {
	y int32
	x byte
	z byte
}

type Bad struct { // want "Bad has size 12 \\(allocator size class 16\\) but the optimal size is 8 leading to a waste of 8 bytes \\(50%\\)"
	x byte
	y int32
	z byte
}

type ZeroGood struct {
	a [0]byte
	b uint32
}

type ZeroBad struct { // want "ZeroBad has size 8 but the optimal size is 4 \\(allocator size class 8\\)"
	a uint32
	b [0]byte
}

type NoNameGood struct {
	Good
	y int32
	x byte
	z byte
}

type NoNameBad struct { // want "NoNameBad has size 20 \\(allocator size class 24\\) but the optimal size is 16 leading to a waste of 8 bytes \\(33%\\)"
	Good
	x byte
	y int32
	z byte
}

type WithComments struct { // want "WithComments has size 8 but the optimal size is 4 \\(allocator size class 8\\)"
	// doc style comment
	a uint32  // field a comment
	b [0]byte // field b comment
	// other doc style comment

	// and a last comment
}
