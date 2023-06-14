package extract

import "context"

type A struct {
	x int
	y int
}

func (a *A) AddP(ctx context.Context) (int, error) {
	sum := a.x + a.y
	return sum, ctx.Err() //@extractmethod("return", "ctx.Err()"),extractfunc("return", "ctx.Err()")
}
