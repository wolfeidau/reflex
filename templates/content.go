package templates

import (
	_ "embed"
)

// InjectedHTML is the HTML injected into the page containing the live reload script.
//
//go:embed injected.html
var InjectedHTML string
