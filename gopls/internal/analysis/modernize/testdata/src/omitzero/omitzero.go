package omitzero

type Foo struct {
	EmptyStruct struct{} `json:",omitempty"` // want "Omitempty has no effect on nested struct fields"
}

type Bar struct {
	NonEmptyStruct struct{ a int } `json:",omitempty"` // want "Omitempty has no effect on nested struct fields"
}

type C struct {
	D string `json:",omitempty"`
}

type R struct {
	M string `json:",omitempty"`
}

type A struct {
	C C `json:"test,omitempty"` // want "Omitempty has no effect on nested struct fields"
	R R `json:"test"`
}

type X struct {
	NonEmptyStruct struct{ a int } `json:",omitempty" yaml:",omitempty"` // want "Omitempty has no effect on nested struct fields"
}

type Y struct {
	NonEmptyStruct struct{ a int } `yaml:",omitempty" json:",omitempty"` // want "Omitempty has no effect on nested struct fields"
}
