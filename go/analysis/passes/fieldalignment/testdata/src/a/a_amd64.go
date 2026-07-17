package a

type PointerGood struct {
	P   *int
	buf [1000]uintptr
}

type PointerBad struct { // want "PointerBad has 8008 leading bytes of pointer data but optimal value is 8"
	buf [1000]uintptr
	P   *int
}

type PointerSorta struct {
	a struct {
		p *int
		q uintptr
	}
	b struct {
		p *int
		q [2]uintptr
	}
}

type PointerSortaBad struct { // want "PointerSortaBad has 32 leading bytes of pointer data but optimal value is 24"
	a struct {
		p *int
		q [2]uintptr
	}
	b struct {
		p *int
		q uintptr
	}
}

type MultiField struct { // want "MultiField has size 40 \\(allocator size class 48\\) but the optimal size is 24 leading to a waste of 24 bytes \\(50%\\)"
	b      bool
	i1, i2 int
	a3     [3]bool
	_      [0]func()
}

type Issue43233 struct { // want "Issue43233 has 88 leading bytes of pointer data but optimal value is 80"
	AllowedEvents []*string // allowed events
	BlockedEvents []*string // blocked events
	APIVersion    string    `mapstructure:"api_version"`
	BaseURL       string    `mapstructure:"base_url"`
	AccessToken   string    `mapstructure:"access_token"`
}
