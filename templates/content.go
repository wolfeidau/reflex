package templates

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"text/template"
)

const ReflexWebsocketAddrEnvKey = "REFLEX_WEBSOCKET_ADDR"

// InjectedHTML is the HTML injected into the page containing the live reload script.
//
//go:embed injected.html
var injectedHTML string

func InjectedHTML() (string, error) {

	websocketAddr := os.Getenv(ReflexWebsocketAddrEnvKey)
	if websocketAddr == "" {
		return "", errors.New("REFLEX_WEBSOCKET_ADDR environment variable not set")
	}

	buf := new(bytes.Buffer)

	tpl, err := template.New("injected.html").Parse(injectedHTML)
	if err != nil {
		return "", fmt.Errorf("failed to parse injected.html: %w", err)
	}

	err = tpl.Execute(buf, map[string]string{
		"WebsocketAddr": websocketAddr,
	})
	if err != nil {
		return "", fmt.Errorf("failed to execute injected.html: %w", err)
	}

	return buf.String(), nil
}
