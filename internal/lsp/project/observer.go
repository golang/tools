package project

import (
	"context"
)

type Observer interface {
	update(event string)
	root() string
	notifyLog(message string)
	notifyError(message string)
	getContext() context.Context
}

type Subject interface {
	notify()
}