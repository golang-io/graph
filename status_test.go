package graph

import (
	"testing"
)

func TestStatusConstants(t *testing.T) {
	tests := []struct {
		status StatusCode
		want   string
	}{
		{PENDING, "PENDING"},
		{RUNNING, "RUNNING"},
		{WAITING, "WAITING"},
		{SUCCEEDED, "SUCCEEDED"},
		{FAILED, "FAILED"},
		{CANCELED, "CANCELED"},
		{SKIPPED, "SKIPPED"},
	}

	for _, tt := range tests {
		if string(tt.status) != tt.want {
			t.Errorf("status string = %q, want %q", tt.status, tt.want)
		}
	}
}

func TestIsTerminal(t *testing.T) {
	terminals := []StatusCode{SUCCEEDED, FAILED, CANCELED, SKIPPED}
	for _, s := range terminals {
		if !IsTerminal(s) {
			t.Errorf("expected %s to be terminal", s)
		}
	}

	nonTerminals := []StatusCode{PENDING, RUNNING, WAITING}
	for _, s := range nonTerminals {
		if IsTerminal(s) {
			t.Errorf("expected %s to NOT be terminal", s)
		}
	}
}
