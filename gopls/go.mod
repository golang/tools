module golang.org/x/tools/gopls

go 1.19 // => default GODEBUG has gotypesalias=0

require (
	github.com/google/go-cmp v0.6.0
	github.com/jba/templatecheck v0.7.0
	golang.org/x/mod v0.19.0
	golang.org/x/sync v0.7.0
	golang.org/x/telemetry v0.0.0-20240607193123-221703e18637
	golang.org/x/text v0.16.0
	golang.org/x/tools v0.21.1-0.20240508182429-e35e4ccd0d2d
	golang.org/x/vuln v1.0.4
	gopkg.in/yaml.v3 v3.0.1
	honnef.co/go/tools v0.4.7
	mvdan.cc/gofumpt v0.6.0
	mvdan.cc/xurls/v2 v2.5.0
)

require (
	github.com/BurntSushi/toml v1.2.1 // indirect
	github.com/google/safehtml v0.1.0 // indirect
	golang.org/x/exp/typeparams v0.0.0-20221212164502-fae10dda9338 // indirect
	golang.org/x/sys v0.22.0 // indirect
	gopkg.in/check.v1 v1.0.0-20190902080502-41f04d3bba15 // indirect

)

replace golang.org/x/tools => ../
