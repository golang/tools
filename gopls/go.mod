module golang.org/x/tools/gopls

go 1.24.2

require (
	github.com/google/go-cmp v0.6.0
	github.com/jba/templatecheck v0.7.1
	golang.org/x/mod v0.24.0
	golang.org/x/sync v0.12.0
	golang.org/x/sys v0.31.0
	golang.org/x/telemetry v0.0.0-20250220152412-165e2f84edbc
	golang.org/x/text v0.23.0
	golang.org/x/tools v0.30.0
	golang.org/x/vuln v1.1.4
	gopkg.in/yaml.v3 v3.0.1
	honnef.co/go/tools v0.6.0
	mvdan.cc/gofumpt v0.7.0
	mvdan.cc/xurls/v2 v2.6.0
)

require (
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c // indirect
	github.com/google/safehtml v0.1.0 // indirect
	golang.org/x/exp/typeparams v0.0.0-20250218142911-aa4b98e5adaa // indirect
	gopkg.in/check.v1 v1.0.0-20190902080502-41f04d3bba15 // indirect
)

replace golang.org/x/tools => ../
