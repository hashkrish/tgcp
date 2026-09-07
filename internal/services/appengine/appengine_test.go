package appengine

import (
	"errors"
	"testing"

	"google.golang.org/api/googleapi"
)

func TestFriendlyServicesError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantOK    bool
		wantTitle string
	}{
		{name: "404 -> not found", err: &googleapi.Error{Code: 404, Message: "not found"}, wantOK: true, wantTitle: "App Engine Not Found"},
		{name: "403 generic -> permission denied", err: &googleapi.Error{Code: 403, Message: "forbidden"}, wantOK: true, wantTitle: "Permission Denied"},
		{
			name:   "403 SERVICE_DISABLED -> admin API disabled",
			err:    &googleapi.Error{Code: 403, Message: "App Engine Admin API has not been used in project my-project before or it is disabled."},
			wantOK: true, wantTitle: "App Engine Admin API Disabled",
		},
		{name: "500 -> not handled, falls back to generic", err: &googleapi.Error{Code: 500, Message: "server error"}, wantOK: false},
		{name: "non-googleapi error -> not handled", err: errors.New("boom"), wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, body, ok := friendlyServicesError(tt.err, "my-project")
			if ok != tt.wantOK {
				t.Fatalf("friendlyServicesError() ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if title != tt.wantTitle {
				t.Errorf("title = %q, want %q", title, tt.wantTitle)
			}
			if body == "" {
				t.Error("body is empty, want an explanation")
			}
		})
	}
}

func TestParseTrafficSplit(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		want      map[string]float64
		wantError bool
	}{
		{name: "single version 100%", in: "v1=100", want: map[string]float64{"v1": 1}},
		{name: "two-way split", in: "v1=80,v2=20", want: map[string]float64{"v1": 0.8, "v2": 0.2}},
		{name: "whitespace tolerated", in: " v1 = 80 , v2 = 20 ", want: map[string]float64{"v1": 0.8, "v2": 0.2}},
		{name: "does not sum to 100", in: "v1=50,v2=40", wantError: true},
		{name: "malformed entry", in: "v1", wantError: true},
		{name: "percent out of range", in: "v1=150", wantError: true},
		{name: "empty input", in: "", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errStr := parseTrafficSplit(tt.in)
			if tt.wantError {
				if errStr == "" {
					t.Fatalf("parseTrafficSplit(%q) = %v, %q; want an error", tt.in, got, errStr)
				}
				return
			}
			if errStr != "" {
				t.Fatalf("parseTrafficSplit(%q) unexpected error: %q", tt.in, errStr)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseTrafficSplit(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("parseTrafficSplit(%q)[%q] = %v, want %v", tt.in, k, got[k], v)
				}
			}
		})
	}
}

func TestFormatSplit(t *testing.T) {
	tests := []struct {
		name  string
		split map[string]float64
		want  string
	}{
		{name: "empty", split: map[string]float64{}, want: "-"},
		{name: "nil", split: nil, want: "-"},
		{name: "single version", split: map[string]float64{"v1": 1}, want: "v1:100%"},
		{name: "two-way split sorted by id", split: map[string]float64{"v2": 0.2, "v1": 0.8}, want: "v1:80% v2:20%"},
		{name: "zero allocations omitted", split: map[string]float64{"v1": 1, "v2": 0}, want: "v1:100%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatSplit(tt.split); got != tt.want {
				t.Errorf("formatSplit(%v) = %q, want %q", tt.split, got, tt.want)
			}
		})
	}
}
