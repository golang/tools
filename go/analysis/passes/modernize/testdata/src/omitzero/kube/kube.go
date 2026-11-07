package kube

type Foo struct {
	EmptyStruct struct{} `json:",omitempty"` // nope: the comment below mentions the k-word
}

// +kubebuilder:validation:Optional
type Other struct {
}
