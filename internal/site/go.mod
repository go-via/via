module go-via.dev/site

go 1.27

require (
	github.com/alecthomas/chroma/v2 v2.27.0
	github.com/go-via/via v0.0.0
)

require github.com/dlclark/regexp2/v2 v2.2.1 // indirect

replace github.com/go-via/via => ../..
