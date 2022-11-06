package c

import "embed"

//go:embed Static/*
var Static embed.FS //@rename("Static", "static")
