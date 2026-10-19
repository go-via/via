module github.com/go-via/via

go 1.27

// Tagged before the release review fixes; use v0.8.1.
retract v0.8.0

require github.com/stretchr/testify v1.11.1

require (
	github.com/BurntSushi/toml v1.4.1-0.20240526193622-a339e1f7089c // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/fzipp/gocyclo v0.6.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/uudashr/gocognit v1.2.0 // indirect
	golang.org/x/exp/typeparams v0.0.0-20231108232855-2478ac86f678 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/tools v0.51.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	honnef.co/go/tools v0.8.2 // indirect
)

tool (
	github.com/fzipp/gocyclo/cmd/gocyclo
	github.com/uudashr/gocognit/cmd/gocognit
	honnef.co/go/tools/cmd/staticcheck
)
