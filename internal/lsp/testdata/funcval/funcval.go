package funcval

/* string */ //@item(string, "string", "", "type")

var (
	stringT string
	ss      string
	ssf     = func() {}
	ssb     bool
)

const (
	ssc = ""
)

type sst struct{}   //@item(sst, "sst", "struct{...}", "struct")
type apPen struct{} //@item(apPen, "apPen", "struct{...}", "struct")

func fn1() sst //@complete("st", sst)

func fn2() apPen //@complete("P", apPen)
