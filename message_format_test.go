package gomesh

import (
	"strings"
	"testing"
)

func TestFormatReplyMessage(t *testing.T) {
	tests := []struct {
		name          string
		replyToID     string
		replyToText   string
		replyToAuthor string
		replyText     string
		expectFormat  string // "enhanced" or "ios"
	}{
		{
			name:          "short reply",
			replyToID:     "msg_123",
			replyToText:   "Hello",
			replyToAuthor: "Alice",
			replyText:     "Hi there!",
			expectFormat:  "enhanced",
		},
		{
			name:          "long reply",
			replyToID:     "msg_123",
			replyToText:   strings.Repeat("x", 200),
			replyToAuthor: "Alice",
			replyText:     strings.Repeat("y", 200),
			expectFormat:  "ios",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatReplyMessage(tt.replyToID, tt.replyToText, tt.replyToAuthor, tt.replyText)

			if tt.expectFormat == "enhanced" {
				if !strings.HasPrefix(result, "🔗") {
					t.Errorf("Expected enhanced format starting with 🔗, got: %s", result)
				}
			} else if tt.expectFormat == "ios" {
				if !strings.HasPrefix(result, "↩️") {
					t.Errorf("Expected iOS format starting with ↩️, got: %s", result)
				}
			}
		})
	}
}

func TestFormatReactionMessage(t *testing.T) {
	tests := []struct {
		name        string
		messageID   string
		emoji       string
		expectStart string
	}{
		{
			name:        "thumbs up reaction",
			messageID:   "msg_123",
			emoji:       "👍",
			expectStart: "👍",
		},
		{
			name:        "heart reaction",
			messageID:   "msg_456",
			emoji:       "❤️",
			expectStart: "❤️",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatReactionMessage(tt.messageID, tt.emoji)

			if !strings.HasPrefix(result, tt.expectStart) {
				t.Errorf("Expected to start with %s, got: %s", tt.expectStart, result)
			}
		})
	}
}

func TestParseMessage_EnhancedReply(t *testing.T) {
	formatted := FormatReplyMessage("msg_123", "Hello", "Alice", "Hi there!")
	parsed := ParseMessage(formatted)

	if parsed.Format != "enhanced" {
		t.Errorf("Expected format 'enhanced', got: %s", parsed.Format)
	}

	if !IsReply(parsed) {
		t.Error("Expected parsed message to be a reply")
	}

	replyToID, replyToText, ok := ExtractReplyMetadata(parsed)
	if !ok {
		t.Error("Failed to extract reply metadata")
	}

	if replyToID != "msg_123" {
		t.Errorf("Expected replyToID 'msg_123', got: %s", replyToID)
	}

	if replyToText != "Hello" {
		t.Errorf("Expected replyToText 'Hello', got: %s", replyToText)
	}

	displayText := GetDisplayText(parsed)
	if displayText != "Hi there!" {
		t.Errorf("Expected display text 'Hi there!', got: %s", displayText)
	}
}

func TestParseMessage_EnhancedReaction(t *testing.T) {
	formatted := FormatReactionMessage("msg_123", "👍")
	parsed := ParseMessage(formatted)

	if parsed.Format != "enhanced" {
		t.Errorf("Expected format 'enhanced', got: %s", parsed.Format)
	}

	if !IsReaction(parsed) {
		t.Error("Expected parsed message to be a reaction")
	}

	messageID, emoji, ok := ExtractReactionMetadata(parsed)
	if !ok {
		t.Error("Failed to extract reaction metadata")
	}

	if messageID != "msg_123" {
		t.Errorf("Expected messageID 'msg_123', got: %s", messageID)
	}

	if emoji != "👍" {
		t.Errorf("Expected emoji '👍', got: %s", emoji)
	}
}

func TestParseMessage_SimpleReaction(t *testing.T) {
	formatted := "👍::msg_123"
	parsed := ParseMessage(formatted)

	if parsed.Format != "simple" {
		t.Errorf("Expected format 'simple', got: %s", parsed.Format)
	}

	if !IsReaction(parsed) {
		t.Error("Expected parsed message to be a reaction")
	}

	messageID, emoji, ok := ExtractReactionMetadata(parsed)
	if !ok {
		t.Error("Failed to extract reaction metadata")
	}

	if messageID != "msg_123" {
		t.Errorf("Expected messageID 'msg_123', got: %s", messageID)
	}

	if emoji != "👍" {
		t.Errorf("Expected emoji '👍', got: %s", emoji)
	}
}

func TestParseMessage_PlainText(t *testing.T) {
	plainText := "This is a plain message"
	parsed := ParseMessage(plainText)

	if parsed.Format != "plain" {
		t.Errorf("Expected format 'plain', got: %s", parsed.Format)
	}

	if IsReply(parsed) {
		t.Error("Expected plain message to not be a reply")
	}

	if IsReaction(parsed) {
		t.Error("Expected plain message to not be a reaction")
	}

	displayText := GetDisplayText(parsed)
	if displayText != plainText {
		t.Errorf("Expected display text '%s', got: %s", plainText, displayText)
	}
}

func TestParseMessage_IOSFormat(t *testing.T) {
	iosFormatted := "↩️ @Alice: Hello\n\nHi there!"
	parsed := ParseMessage(iosFormatted)

	if parsed.Format != "ios" {
		t.Errorf("Expected format 'ios', got: %s", parsed.Format)
	}

	if !IsReply(parsed) {
		t.Error("Expected parsed message to be a reply")
	}

	displayText := GetDisplayText(parsed)
	if displayText != "Hi there!" {
		t.Errorf("Expected display text 'Hi there!', got: %s", displayText)
	}
}

func TestMessageSizeLimit(t *testing.T) {
	// Test that very long messages fall back to iOS format
	longText := strings.Repeat("x", 300)
	formatted := FormatReplyMessage("msg_123", longText, "Alice", longText)

	if !strings.HasPrefix(formatted, "↩️") {
		t.Error("Expected long message to fall back to iOS format")
	}
}

