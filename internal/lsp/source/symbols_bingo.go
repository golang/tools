package source

import (
	"context"
	"strings"
)

func Symbols(ctx context.Context, view View, query string, limit int) []Symbol {
	var symbols []Symbol
	f := func(pkg Package) bool {
		if ctx.Err() != nil {
			return true
		}

		for _, file := range pkg.GetSyntax(ctx) {
			astSymbols, _ := getSymbols(view.FileSet(), file, pkg)
			for _, symbol := range astSymbols {
				if len(symbols) >= limit {
					return true
				}

				if strings.Contains(symbol.Name, query) {
					symbols = append(symbols, symbol)
				} else if strings.Contains(pkg.GetTypes().Name()+"."+symbol.Name, query) {
					symbols = append(symbols, symbol)
				}
			}
		}

		return false
	}
	view.Search()(f)
	return symbols
}
