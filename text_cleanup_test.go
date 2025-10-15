package gomesh

import (
	"testing"
)

func TestCleanANSIEscapeSequences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Simple ANSI color codes",
			input:    "\x1b[34mDEBUG\x1b[0m | 12:23:47 67 [SerialConsole] Send known nodes",
			expected: "DEBUG | 12:23:47 67 [SerialConsole] Send known nodes",
		},
		{
			name:     "Complex ANSI sequences",
			input:    "\x1b[1;32mINFO\x1b[0m\x1b[34m text \x1b[0m",
			expected: "INFO text ",
		},
		{
			name:     "Multiple escape sequences",
			input:    "\x1b[0m\x1b[34mDEBUG\x1b[0m\x1b[33m message\x1b[0m",
			expected: "DEBUG message",
		},
		{
			name:     "No escape sequences",
			input:    "Plain text message",
			expected: "Plain text message",
		},
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanANSIEscapeSequences(tt.input)
			if result != tt.expected {
				t.Errorf("cleanANSIEscapeSequences() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestCleanControlCharacters(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Remove control characters but keep printable",
			input:    "Hello\x00\x01World\x02",
			expected: "HelloWorld",
		},
		{
			name:     "Keep tabs and newlines",
			input:    "Line1\nLine2\tTabbed",
			expected: "Line1\nLine2\tTabbed",
		},
		{
			name:     "Remove bell and other controls",
			input:    "Text\x07\x08\x0BMore",
			expected: "TextMore",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cleanControlCharacters(tt.input)
			if result != tt.expected {
				t.Errorf("cleanControlCharacters() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExtractTextFromBytes(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []string
	}{
		{
			name:     "Meshtastic debug output with ANSI",
			input:    []byte("\x1b[34mDEBUG\x1b[0m | 12:23:47 67 [SerialConsole] \x1b[34mSend known nodes\x1b[0m\r\n"),
			expected: []string{"DEBUG | 12:23:47 67 [SerialConsole] Send known nodes"},
		},
		{
			name:     "Multiple lines with escape sequences and debug patterns",
			input:    []byte("INFO | 12:23:47\x1b[34m\r\nDEBUG | 12:23:48\x1b[0m\r\n"),
			expected: []string{"INFO | 12:23:47", "DEBUG | 12:23:48"},
		},
		{
			name:     "Empty input",
			input:    []byte{},
			expected: nil,
		},
		{
			name:     "Only control characters",
			input:    []byte("\x00\x01\x02\x1b[0m"),
			expected: nil,
		},
		{
			name:     "Text without debug patterns (should be ignored)",
			input:    []byte("Valid text\x00\x01\r\nAnother line\x1b[34m"),
			expected: nil, // Changed: no debug patterns, so not treated as text
		},
		{
			name:     "SerialConsole pattern",
			input:    []byte("Some data [SerialConsole] message here\r\n"),
			expected: []string{"Some data [SerialConsole] message here"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTextFromBytes(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("extractTextFromBytes() returned %d lines, want %d", len(result), len(tt.expected))
				t.Errorf("Got: %v", result)
				t.Errorf("Want: %v", tt.expected)
				return
			}
			for i, line := range result {
				if line != tt.expected[i] {
					t.Errorf("extractTextFromBytes() line %d = %q, want %q", i, line, tt.expected[i])
				}
			}
		})
	}
}

func TestIsPrintableText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "Normal text",
			input:    "Hello World",
			expected: true,
		},
		{
			name:     "Text with spaces and tabs",
			input:    "Text\twith\ttabs and spaces",
			expected: true,
		},
		{
			name:     "Mostly control characters",
			input:    "\x00\x01\x02ABC",
			expected: false,
		},
		{
			name:     "Empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "Only spaces",
			input:    "   ",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isPrintableText(tt.input)
			if result != tt.expected {
				t.Errorf("isPrintableText(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}
