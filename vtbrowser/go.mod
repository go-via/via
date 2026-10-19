module github.com/go-via/via/vtbrowser

go 1.27

require (
	github.com/chromedp/cdproto v0.157.8
	github.com/chromedp/chromedp v0.21.0
	github.com/go-via/via v0.9.1
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/go-json-experiment/json v0.0.0-20260213210345-44df1a37e875 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/tools v0.51.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	honnef.co/go/tools v0.8.2 // indirect
)

replace github.com/go-via/via => ../

tool honnef.co/go/tools/cmd/staticcheck
