package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErr    bool
		wantStdout string
	}{
		{name: "sin comando", args: nil, wantErr: true},
		{name: "comando desconocido", args: []string{"bailar"}, wantErr: true},
		{name: "version", args: []string{"version"}, wantStdout: "dev"},
		{name: "ayuda", args: []string{"help"}, wantStdout: "uso: maestro"},
		{name: "flag desconocido", args: []string{"serve", "--nada"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(context.Background(), tt.args, &stdout, &stderr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("run(%q) error = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, quería que contenga %q", stdout.String(), tt.wantStdout)
			}
		})
	}
}

func TestVersionLdflags(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })
	version = "v1.2.3"
	if got := currentVersion(); got != "v1.2.3" {
		t.Errorf("currentVersion() = %q, quería v1.2.3", got)
	}
}
