package source

// WalkFunc walk function
type WalkFunc func(p Package) bool

type Cache interface {
	Walk(walkFunc WalkFunc, ranks []string)
}


