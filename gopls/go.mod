module golang.org/x/tools/gopls

go 1.18

require (
	github.com/google/go-cmp v0.5.9
	github.com/jba/printsrc v0.2.2
	github.com/jba/templatecheck v0.6.0
	golang.org/x/mod v0.14.0
	golang.org/x/sync v0.5.0
	golang.org/x/telemetry v0.0.0-20231114163143-69313e640400
	golang.org/x/text v0.14.0
	golang.org/x/tools v0.13.1-0.20230920233436-f9b8da7b22be
	golang.org/x/vuln v1.0.1
	gopkg.in/yaml.v3 v3.0.1
	honnef.co/go/tools v0.4.5
	mvdan.cc/gofumpt v0.4.0
	mvdan.cc/xurls/v2 v2.4.0
)

require (
	github.com/BurntSushi/toml v1.2.1 // indirect
	github.com/google/safehtml v0.1.0 // indirect
	golang.org/x/exp/typeparams v0.0.0-20221212164502-fae10dda9338 // indirect
	golang.org/x/sys v0.15.0 // indirect
	gopkg.in/check.v1 v1.0.0-20190902080502-41f04d3bba15 // indirect

)

replace golang.org/x/tools => ../
