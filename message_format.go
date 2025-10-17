package gomesh

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// MessageMetadata contains reply and reaction information
type MessageMetadata struct {
	ReplyTo   string `json:"r,omitempty"` // Message ID being replied to
	Type      string `json:"t,omitempty"` // "reply" or "reaction"
	Reaction  string `json:"e,omitempty"` // Emoji for reactions
	ReplyText string `json:"rt,omitempty"` // Original message text for iOS fallback
}

// ParsedMessage represents a message with extracted metadata
type ParsedMessage struct {
	Text     string
	Metadata *MessageMetadata
	Format   string // "enhanced", "ios", "simple", "plain"
}

// FormatReplyMessage creates a reply message with enhanced format and iOS fallback
// Enhanced format: 🔗{"r":"msgId","t":"reply"}actual message text
// iOS fallback: ↩️ @username: original message\n\nReply text
func FormatReplyMessage(replyToID string, replyToText string, replyToAuthor string, replyText string) string {
	// Create metadata
	metadata := MessageMetadata{
		ReplyTo:   replyToID,
		Type:      "reply",
		ReplyText: replyToText,
	}

	// Marshal metadata to JSON
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		// Fallback to plain text if JSON marshaling fails
		return fmt.Sprintf("↩️ @%s: %s\n\n%s", replyToAuthor, replyToText, replyText)
	}

	// Enhanced format: 🔗{metadata}actual message text
	enhanced := fmt.Sprintf("🔗%s%s", string(metadataJSON), replyText)

	// Check if message fits within typical LoRA limits (240 bytes)
	if len(enhanced) <= 240 {
		return enhanced
	}

	// If too long, fall back to iOS format
	return fmt.Sprintf("↩️ @%s: %s\n\n%s", replyToAuthor, replyToText, replyText)
}

// FormatReactionMessage creates a reaction message
// Enhanced format: 👍{"r":"msgId","t":"reaction","e":"emoji"}
// Simple format: 👍::messageId
func FormatReactionMessage(messageID string, emoji string) string {
	// Create metadata
	metadata := MessageMetadata{
		ReplyTo:  messageID,
		Type:     "reaction",
		Reaction: emoji,
	}

	// Marshal metadata to JSON
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		// Fallback to simple format
		return fmt.Sprintf("%s::%s", emoji, messageID)
	}

	// Enhanced format: emoji{metadata}
	enhanced := fmt.Sprintf("%s%s", emoji, string(metadataJSON))

	// Check if message fits within typical LoRA limits
	if len(enhanced) <= 240 {
		return enhanced
	}

	// Fallback to simple format
	return fmt.Sprintf("%s::%s", emoji, messageID)
}

// ParseMessage parses a message and extracts metadata
// Supports: enhanced format, iOS format, simple reactions, and plain text
func ParseMessage(text string) *ParsedMessage {
	result := &ParsedMessage{
		Text:   text,
		Format: "plain",
	}

	// Try to parse enhanced format (starts with emoji followed by JSON)
	if strings.HasPrefix(text, "🔗") || strings.HasPrefix(text, "👍") ||
		strings.HasPrefix(text, "❤️") || strings.HasPrefix(text, "😂") ||
		strings.HasPrefix(text, "😢") || strings.HasPrefix(text, "🔥") {

		// Extract emoji and potential JSON
		emoji := string([]rune(text)[0])
		rest := strings.TrimPrefix(text, emoji)

		// Try to parse JSON metadata
		var metadata MessageMetadata
		jsonEnd := strings.Index(rest, "}")
		if jsonEnd > 0 {
			jsonStr := rest[:jsonEnd+1]
			if err := json.Unmarshal([]byte(jsonStr), &metadata); err == nil {
				result.Metadata = &metadata
				result.Text = strings.TrimSpace(rest[jsonEnd+1:])

				if metadata.Type == "reply" {
					result.Format = "enhanced"
					return result
				} else if metadata.Type == "reaction" {
					result.Format = "enhanced"
					return result
				}
			}
		}
	}

	// Try to parse simple reaction format: emoji::messageId
	simpleReactionRegex := regexp.MustCompile(`^(.)\:\:(.+)$`)
	if matches := simpleReactionRegex.FindStringSubmatch(text); matches != nil {
		result.Metadata = &MessageMetadata{
			ReplyTo:  matches[2],
			Type:     "reaction",
			Reaction: matches[1],
		}
		result.Format = "simple"
		result.Text = ""
		return result
	}

	// Try to parse iOS format: ↩️ @username: original message\n\nReply text
	if strings.HasPrefix(text, "↩️") {
		iosRegex := regexp.MustCompile(`^↩️\s+@(.+?):\s+(.+?)\n\n(.+)$`)
		if matches := iosRegex.FindStringSubmatch(text); matches != nil {
			result.Metadata = &MessageMetadata{
				Type:      "reply",
				ReplyText: matches[2],
			}
			result.Text = matches[3]
			result.Format = "ios"
			return result
		}
	}

	// Plain text message
	result.Format = "plain"
	return result
}

// ExtractReplyMetadata extracts reply information from a parsed message
func ExtractReplyMetadata(parsed *ParsedMessage) (replyToID string, replyToText string, ok bool) {
	if parsed == nil || parsed.Metadata == nil {
		return "", "", false
	}

	if parsed.Metadata.Type == "reply" {
		return parsed.Metadata.ReplyTo, parsed.Metadata.ReplyText, true
	}

	return "", "", false
}

// ExtractReactionMetadata extracts reaction information from a parsed message
func ExtractReactionMetadata(parsed *ParsedMessage) (messageID string, emoji string, ok bool) {
	if parsed == nil || parsed.Metadata == nil {
		return "", "", false
	}

	if parsed.Metadata.Type == "reaction" {
		return parsed.Metadata.ReplyTo, parsed.Metadata.Reaction, true
	}

	return "", "", false
}

// GetDisplayText returns the text that should be displayed to users
// For replies, this is the reply text
// For reactions, this is empty (reactions are displayed separately)
// For plain messages, this is the original text
func GetDisplayText(parsed *ParsedMessage) string {
	if parsed == nil {
		return ""
	}

	switch parsed.Format {
	case "enhanced", "ios":
		return parsed.Text
	case "simple":
		return "" // Reactions don't have display text
	default:
		return parsed.Text
	}
}

// IsReply checks if a parsed message is a reply
func IsReply(parsed *ParsedMessage) bool {
	return parsed != nil && parsed.Metadata != nil && parsed.Metadata.Type == "reply"
}

// IsReaction checks if a parsed message is a reaction
func IsReaction(parsed *ParsedMessage) bool {
	return parsed != nil && parsed.Metadata != nil && parsed.Metadata.Type == "reaction"
}

