package gomesh

import (
	"encoding/hex"
	"testing"
)

func TestIsTextData(t *testing.T) {
	tests := []struct {
		name     string
		input    string // hex string
		expected bool
	}{
		{
			name:     "Debug message with ANSI escape sequences",
			input:    "346d4445425547201b5b306d7c2031323a32323a3437203637205b53657269616c436f6e736f6c655d201b5b33346d",
			expected: true,
		},
		{
			name:     "Send known nodes message",
			input:    "53656e64206b6e6f776e206e6f6465730d0a",
			expected: true,
		},
		{
			name:     "Time pattern message",
			input:    "31323a32323a3437",
			expected: true,
		},
		{
			name:     "Valid protobuf data",
			input:    "0801120548656c6c6f",
			expected: false,
		},
		{
			name:     "Binary data",
			input:    "deadbeefcafebabe",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := hex.DecodeString(tt.input)
			if err != nil {
				t.Fatalf("Failed to decode hex string: %v", err)
			}

			result := isTextData(data)
			if result != tt.expected {
				t.Errorf("isTextData() = %v, expected %v for input: %s", result, tt.expected, string(data))
			}
		})
	}
}

func TestIsLikelyFalsePacketHeader(t *testing.T) {
	tests := []struct {
		name     string
		input    string // hex string
		expected bool
	}{
		{
			name:     "False header with debug content",
			input:    "94c3005b346d4445425547201b5b306d7c2031323a32323a3437",
			expected: true,
		},
		{
			name:     "Valid protobuf packet header",
			input:    "94c300080801120548656c6c6f",
			expected: false,
		},
		{
			name:     "Too short to determine",
			input:    "94c300",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := hex.DecodeString(tt.input)
			if err != nil {
				t.Fatalf("Failed to decode hex string: %v", err)
			}

			result := isLikelyFalsePacketHeader(data)
			if result != tt.expected {
				t.Errorf("isLikelyFalsePacketHeader() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
