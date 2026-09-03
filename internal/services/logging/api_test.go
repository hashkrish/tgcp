package logging

import (
	"testing"
	"time"
)

// TestCleanPayload_StripsDockerProgressControlChars covers a real bug: Docker/
// buildkit push-progress lines embed bare "\r" (redraw-in-place) and ANSI CSI
// escape sequences (cursor movement, erase-line). Passed through unstripped,
// they got interpreted by the terminal rendering tgcp itself, visually
// truncating/corrupting unrelated text -- not a table/column truncation bug.
func TestCleanPayload_StripsDockerProgressControlChars(t *testing.T) {
	ts := time.Now()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bare carriage return becomes newline",
			in:   "The push refers to repository [x]\r75e926a1b2c3: Preparing",
			want: "The push refers to repository [x]\n75e926a1b2c3: Preparing",
		},
		{
			name: "CRLF collapses to a single newline",
			in:   "line one\r\nline two",
			want: "line one\nline two",
		},
		{
			name: "ANSI cursor-up and erase-line sequences are stripped",
			in:   "\x1b[1A\x1b[2K75e926a1b2c3: Layer already exists",
			want: "75e926a1b2c3: Layer already exists",
		},
		{
			name: "ANSI color codes are stripped",
			in:   "\x1b[32mSUCCESS\x1b[0m",
			want: "SUCCESS",
		},
		{
			name: "combined CR-redraw plus ANSI, matching real docker push output",
			in:   "75e926a1b2c3: Waiting\r\x1b[1A\x1b[2K75e926a1b2c3: Preparing\r\x1b[1A\x1b[2K75e926a1b2c3: Layer already exists",
			want: "75e926a1b2c3: Waiting\n75e926a1b2c3: Preparing\n75e926a1b2c3: Layer already exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanPayload(tt.in, ts); got != tt.want {
				t.Errorf("cleanPayload(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
