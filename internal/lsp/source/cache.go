package source

// WalkFunc walk function
type WalkFunc func(p Package) bool

type ICache interface {
	Walk(walkFunc WalkFunc)
}


