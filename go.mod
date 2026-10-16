module github.com/tevfik/gleann-plugin-docs

go 1.26.0

require (
	github.com/tevfik/gleann/modules/chunking v0.0.0
	github.com/yalue/onnxruntime_go v1.26.0
	golang.org/x/image v0.30.0
)

require (
	github.com/smacker/go-tree-sitter v0.0.0-20240827094217-dd81d9e9be82 // indirect
	golang.org/x/net v0.61.0 // indirect
)

replace github.com/tevfik/gleann/modules/chunking => ../../gleann/modules/chunking
