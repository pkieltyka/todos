// Package github implements the GitHub read backend using go-gh API clients.
package github

import (
	"embed"
	"fmt"
	"regexp"
)

//go:embed queries/*.graphql
var queryFiles embed.FS

var operationName = regexp.MustCompile(`^(?:query|mutation)\s+(\w+)`)
var documents = loadDocuments()

func loadDocuments() map[string]string {
	files, err := queryFiles.ReadDir("queries")
	if err != nil {
		panic(err)
	}
	out := make(map[string]string, len(files))
	for _, file := range files {
		body, err := queryFiles.ReadFile("queries/" + file.Name())
		if err != nil {
			panic(err)
		}
		match := operationName.FindStringSubmatch(string(body))
		if len(match) != 2 {
			panic("invalid embedded GraphQL document: " + file.Name())
		}
		if _, exists := out[match[1]]; exists {
			panic("duplicate GraphQL operation: " + match[1])
		}
		out[match[1]] = string(body)
	}
	return out
}

func document(name string) (string, error) {
	if text, ok := documents[name]; ok {
		return text, nil
	}
	return "", fmt.Errorf("unknown embedded GraphQL operation %q", name)
}
