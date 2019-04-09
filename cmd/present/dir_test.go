package main

import "testing"

func TestRepository(t *testing.T)  {
	v := repository("github.com/corylanou/go-mongo-presentation/presentation.slide")
	if v != "github.com/corylanou/go-mongo-presentation" {
		t.Error(v)
	}
	v = repository("github.com/corylanou/go-mongo-presentation")
	if v != "github.com/corylanou/go-mongo-presentation" {
		t.Error(v)
	}
	v = repository("github.com/corylanou/")
	if v != "" {
		t.Error(v)
	}
	v = repository("github.com/corylanou")
	if v != "" {
		t.Error(v)
	}
	v = repository("xxx/corylanou/go-mongo-presentation")
	if v != "" {
		t.Error(v)
	}
}
