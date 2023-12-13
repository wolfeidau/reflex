package templates

import (
	"os"
	"strings"
	"testing"
)

func TestInjectedHTML(t *testing.T) {
	tests := []struct {
		name    string
		envVar  string
		want    string
		wantErr bool
	}{
		{
			name:   "success",
			envVar: "localhost:1234",
			want:   `var address = 'ws://' + 'localhost:1234' + '/ws';`,
		},
		{
			name:    "missing env var",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVar != "" {
				err := os.Setenv(ReflexWebsocketAddrEnvKey, tt.envVar)
				if err != nil {
					t.Fatal(err)
				}
				defer os.Unsetenv(ReflexWebsocketAddrEnvKey)
			}

			got, err := InjectedHTML()
			if (err != nil) != tt.wantErr {
				t.Errorf("InjectedHTML() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !strings.Contains(got, tt.want) {
				t.Errorf("InjectedHTML() got = %v, want %v", got, tt.want)
			}
		})
	}
}
