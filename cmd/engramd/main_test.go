package main

import (
	"testing"

	"github.com/Fraancuus/engram"
)

func TestDecayFromEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		env      map[string]string
		want     engram.DecayModel
		wantName string
		wantErr  bool
	}{
		{name: "unset defaults to type-aware", env: nil, want: engram.TypeAwareDecay{}, wantName: "type-aware"},
		{name: "empty defaults to type-aware", env: map[string]string{"ENGRAM_DECAY": ""}, want: engram.TypeAwareDecay{}, wantName: "type-aware"},
		{name: "uniform", env: map[string]string{"ENGRAM_DECAY": "uniform"}, want: engram.UniformDecay{}, wantName: "uniform"},
		{name: "unknown errors", env: map[string]string{"ENGRAM_DECAY": "linear"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, name, err := decayFromEnv(func(k string) string { return tt.env[k] })
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if got != tt.want || name != tt.wantName {
				t.Errorf("got (%v, %q), want (%v, %q)", got, name, tt.want, tt.wantName)
			}
		})
	}
}
