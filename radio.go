package gomesh

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	pb "github.com/b7r-dev/goMesh/github.com/meshtastic/gomeshproto"
	"google.golang.org/protobuf/proto"
)

// min returns the smaller of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// validatePacketStructure performs basic validation on a received packet
func validatePacketStructure(packet []byte) error {
	if len(packet) < headerLen {
		return errors.New("packet too short for header")
	}

	if packet[0] != start1 || packet[1] != start2 {
		return errors.New("invalid packet header")
	}

	expectedLength := int((packet[2] << 8) + packet[3])
	actualPayloadLength := len(packet) - headerLen

	if actualPayloadLength != expectedLength {
		return errors.New(fmt.Sprintf("length mismatch: expected %d, got %d", expectedLength, actualPayloadLength))
	}

	if expectedLength > maxToFromRadioSzie {
		return errors.New(fmt.Sprintf("packet too large: %d > %d", expectedLength, maxToFromRadioSzie))
	}

	return nil
}

const start1 = byte(0x94)
const start2 = byte(0xc3)
const headerLen = 4
const maxToFromRadioSzie = 512
const broadcastAddr = "^all"
const localAddr = "^local"
const defaultHopLimit = 3
const broadcastNum = 0xffffffff

// ResponseType indicates the type of data received from the radio
type ResponseType int

const (
	ResponseTypeProtobuf ResponseType = iota
	ResponseTypeText
	ResponseTypeUnknown
)

// RadioResponse represents a response from the radio that can be either protobuf or text
type RadioResponse struct {
	Type        ResponseType
	ProtobufMsg *pb.FromRadio
	TextData    string
	RawBytes    []byte
}

// RadioResponseSet contains both protobuf and text responses from a read operation
type RadioResponseSet struct {
	ProtobufPackets []*pb.FromRadio
	TextMessages    []string
	AllResponses    []*RadioResponse
}

// isTextData determines if the given bytes represent text data
// This function is now VERY conservative - only identifies data as text when absolutely certain
func isTextData(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	// Convert to string for pattern matching
	dataStr := string(data)

	// ONLY identify as text if we find EXPLICIT console/debug patterns
	// Do NOT use ASCII ratio heuristics as they incorrectly flag protobuf data
	debugPatterns := []string{
		"DEBUG", "INFO", "WARN", "ERROR", "TRACE",
		"SerialConsole", "Send known nodes",
		"\x1b[",                                                                                                    // ANSI escape sequences
		"| 12:", "| 13:", "| 14:", "| 15:", "| 16:", "| 17:", "| 18:", "| 19:", "| 20:", "| 21:", "| 22:", "| 23:", // Time patterns with pipe
	}

	// Must contain at least one explicit debug pattern
	hasDebugPattern := false
	for _, pattern := range debugPatterns {
		if strings.Contains(dataStr, pattern) {
			hasDebugPattern = true
			break
		}
	}

	if !hasDebugPattern {
		return false
	}

	// Additional validation: if it has debug patterns, it should also have reasonable text characteristics
	// But be much more lenient than before
	printableCount := 0
	for _, b := range data {
		if unicode.IsPrint(rune(b)) || b == '\n' || b == '\r' || b == '\t' || b == ' ' {
			printableCount++
		}
	}

	// Only require 50% printable characters (much more lenient)
	// This catches cases where debug output is mixed with some binary data
	if len(data) > 0 {
		printableRatio := float64(printableCount) / float64(len(data))
		return printableRatio > 0.5
	}

	return false
}

// isLikelyFalsePacketHeader checks if what looks like a packet header is actually text data
func isLikelyFalsePacketHeader(data []byte) bool {
	if len(data) < 8 {
		return false
	}

	// Check if the "payload" portion contains obvious text patterns
	if len(data) > 4 {
		payloadPortion := data[4:]
		if isTextData(payloadPortion) {
			return true
		}
	}

	return false
}

// extractTextFromBytes extracts text data from a byte buffer, handling common text patterns and cleaning escape sequences
func extractTextFromBytes(data []byte) []string {
	if len(data) == 0 {
		return nil
	}

	// Convert to string and clean up control characters
	originalText := string(data)
	text := originalText

	// Clean up ANSI escape sequences (e.g., \x1b[0m, \x1b[34m, etc.)
	text = cleanANSIEscapeSequences(text)

	// Normalize line endings
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	// Remove other common control characters but keep printable ones
	text = cleanControlCharacters(text)

	// Text cleanup completed silently

	lines := strings.Split(text, "\n")
	var validLines []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) > 0 && isPrintableText(line) {
			validLines = append(validLines, line)
		}
	}

	return validLines
}

// cleanANSIEscapeSequences removes ANSI escape sequences from text
func cleanANSIEscapeSequences(text string) string {
	// Remove ANSI escape sequences like \x1b[0m, \x1b[34m, \x1b[1;32m, etc.
	// Pattern: ESC [ followed by any number of digits, semicolons, and ends with a letter
	ansiPattern := `\x1b\[[0-9;]*[a-zA-Z]`
	re := regexp.MustCompile(ansiPattern)
	text = re.ReplaceAllString(text, "")

	// Also remove standalone ESC characters that might be left over
	text = strings.ReplaceAll(text, "\x1b", "")

	return text
}

// cleanControlCharacters removes non-printable control characters but keeps useful ones
func cleanControlCharacters(text string) string {
	var result strings.Builder

	for _, r := range text {
		// Keep printable characters, newlines, tabs, and spaces
		if unicode.IsPrint(r) || r == '\n' || r == '\t' || r == ' ' {
			result.WriteRune(r)
		}
		// Skip other control characters (0x00-0x1F except \n, \t)
	}

	return result.String()
}

// isPrintableText checks if a line contains mostly printable characters
func isPrintableText(line string) bool {
	if len(line) == 0 {
		return false
	}

	printableCount := 0
	for _, r := range line {
		if unicode.IsPrint(r) || r == ' ' || r == '\t' {
			printableCount++
		}
	}

	// Require at least 80% printable characters
	return float64(printableCount)/float64(len(line)) >= 0.8
}

// Radio holds the port and serial io.ReadWriteCloser struct to maintain one serial connection
type Radio struct {
	streamer streamer
	nodeNum  uint32
}

// Init initializes the Serial connection for the radio
func (r *Radio) Init(port string) error {

	streamer := streamer{}
	err := streamer.Init(port)
	if err != nil {
		return err
	}
	r.streamer = streamer

	// Switch radio from console mode to API mode
	log.Printf("🔄 RADIO INIT: Switching to API mode...")
	err = r.switchToAPIMode()
	if err != nil {
		log.Printf("❌ RADIO INIT: Failed to switch to API mode: %v", err)
		return err
	}
	log.Printf("✅ RADIO INIT: Successfully switched to API mode")

	err = r.getNodeNum()
	if err != nil {
		return err
	}

	return nil
}

// GetNodeID returns the node ID of the connected radio
func (r *Radio) GetNodeID() uint32 {
	return r.nodeNum
}

// switchToAPIMode switches the radio from console mode to API (protobuf) mode
func (r *Radio) switchToAPIMode() error {
	log.Printf("📤 SWITCHING TO API MODE: Sending exit command...")

	// Send "exit" command to exit console mode and switch to API mode
	// This is the standard way to switch Meshtastic radios from console to API mode
	exitCommand := []byte("exit\n")
	err := r.streamer.Write(exitCommand)
	if err != nil {
		return err
	}

	// Wait a bit for the mode switch to take effect
	time.Sleep(500 * time.Millisecond)

	// Clear any remaining console output from the buffer
	for i := 0; i < 10; i++ {
		b := make([]byte, 1024)
		err := r.streamer.Read(b)
		if err != nil {
			// If we get a timeout or EOF, that's expected - buffer is clear
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Send additional commands to ensure we're fully in API mode
	// Some firmware versions may still output debug messages after exit
	// Try multiple approaches to disable console output - be more aggressive
	commands := []string{
		"set device.debug_log_enabled false\n",
		"set device.serial_enabled false\n",
		"set serial.enabled false\n",
		"set device.serial_console_enabled false\n",
		"set console.enabled false\n",
		"set device.debug_log_api_enabled false\n",
		"set device.debug_log_radio_enabled false\n",
		"set device.debug_log_gps_enabled false\n",
		"set device.debug_log_mesh_enabled false\n",
		"set device.debug_log_modules_enabled false\n",
		"exit\n", // Send exit again to be sure
		"exit\n", // Send exit twice to be extra sure
	}

	for _, cmd := range commands {
		err = r.streamer.Write([]byte(cmd))
		if err == nil {
			// Wait a bit for each command to take effect
			time.Sleep(200 * time.Millisecond)

			// Clear any response from the command
			for j := 0; j < 3; j++ {
				b := make([]byte, 512)
				err := r.streamer.Read(b)
				if err != nil {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
	}
	return nil
}

// sendPacket takes a protbuf packet, construct the appropriate header and sends it to the radio
func (r *Radio) sendPacket(protobufPacket []byte) (err error) {

	packageLength := len(protobufPacket) // FIXED: Don't convert to string, which corrupts binary data

	header := []byte{start1, start2, byte(packageLength>>8) & 0xff, byte(packageLength) & 0xff}

	radioPacket := append(header, protobufPacket...)

	// Send packet to radio

	err = r.streamer.Write(radioPacket)
	if err != nil {
		log.Printf("❌ PACKET SEND FAILED: %v", err)
		return err
	}

	log.Printf("✅ PACKET SENT SUCCESSFULLY")
	return

}

// ReadResponseWithTypes reads responses from the serial port and returns both text and protobuf data
func (r *Radio) ReadResponseWithTypes(timeout bool) (*RadioResponseSet, error) {
	log.Printf("📥 READRESPONSE_ENHANCED: Starting to read radio response (timeout=%v)", timeout)

	b := make([]byte, 1)
	emptyByte := make([]byte, 0)
	processedBytes := make([]byte, 0)
	textBuffer := make([]byte, 0)
	repeatByteCounter := 0
	previousByte := make([]byte, 1)
	totalBytesRead := 0

	responseSet := &RadioResponseSet{
		ProtobufPackets: make([]*pb.FromRadio, 0),
		TextMessages:    make([]string, 0),
		AllResponses:    make([]*RadioResponse, 0),
	}

	for {
		err := r.streamer.Read(b)
		if err == nil {
			totalBytesRead++
			// Suppress all byte-by-byte logging to reduce noise
		}

		if bytes.Equal(b, previousByte) {
			repeatByteCounter++
		} else {
			repeatByteCounter = 0
		}

		if err == io.EOF || repeatByteCounter > 20 || errors.Is(err, os.ErrDeadlineExceeded) {
			log.Printf("📥 READRESPONSE_ENHANCED: Breaking loop - EOF=%v, RepeatCount=%d, Timeout=%v, TotalBytes=%d",
				err == io.EOF, repeatByteCounter, errors.Is(err, os.ErrDeadlineExceeded), totalBytesRead)
			break
		} else if err != nil {
			log.Printf("❌ READRESPONSE_ENHANCED: Read error: %v", err)
			return nil, err
		}
		copy(previousByte, b)

		if len(b) > 0 {
			// Try to detect if we're in a protobuf packet sequence
			pointer := len(processedBytes)

			// Check if this byte could be the start of a protobuf packet
			if pointer == 0 && b[0] == start1 {
				// Process any accumulated text data before starting protobuf parsing
				if len(textBuffer) > 0 && isTextData(textBuffer) {
					textLines := extractTextFromBytes(textBuffer)
					if len(textLines) > 0 {
						// Completely suppress text data logging to reduce noise
						for _, line := range textLines {
							responseSet.TextMessages = append(responseSet.TextMessages, line)
							responseSet.AllResponses = append(responseSet.AllResponses, &RadioResponse{
								Type:     ResponseTypeText,
								TextData: line,
								RawBytes: []byte(line),
							})
						}
					}
					textBuffer = emptyByte
				}

				// Start protobuf packet processing
				processedBytes = append(processedBytes, b...)
				log.Printf("🔍 HEADER: Found START1 (0x%02x)", b[0])
			} else if pointer == 1 && b[0] == start2 {
				// Continue protobuf packet processing
				processedBytes = append(processedBytes, b...)
				log.Printf("🔍 HEADER: Found START2 (0x%02x)", b[0])
			} else if pointer > 0 && pointer < headerLen {
				// Continue building protobuf header
				processedBytes = append(processedBytes, b...)
			} else if pointer >= headerLen {
				// We're in protobuf payload processing
				processedBytes = append(processedBytes, b...)

				packetLength := int((processedBytes[2] << 8) + processedBytes[3])
				if pointer == headerLen {
					log.Printf("🔍 PACKET LENGTH: Calculated length=%d (bytes 2-3: 0x%02x 0x%02x)",
						packetLength, processedBytes[2], processedBytes[3])
					if packetLength > maxToFromRadioSzie {
						log.Printf("❌ PACKET TOO LARGE: %d > %d - resetting", packetLength, maxToFromRadioSzie)
						processedBytes = emptyByte
						textBuffer = append(textBuffer, b...)
						continue
					}

					// Check if this might be a false packet header (debug output that accidentally looks like a header)
					if len(processedBytes) >= 8 && isLikelyFalsePacketHeader(processedBytes) {
						log.Printf("🔍 FALSE HEADER DETECTED: Treating as text data")
						textBuffer = append(textBuffer, processedBytes...)
						processedBytes = emptyByte
						continue
					}
				}

				if len(processedBytes) != 0 && pointer+1 == packetLength+headerLen {
					// Complete protobuf packet received
					payloadBytes := processedBytes[headerLen:]

					log.Printf("🔍 PARSING PROTOBUF: TotalLen=%d, HeaderLen=%d, PayloadLen=%d, ExpectedLen=%d",
						len(processedBytes), headerLen, len(payloadBytes), packetLength)

					if len(payloadBytes) == 0 {
						log.Printf("⚠️  EMPTY PAYLOAD: Skipping empty protobuf payload")
						processedBytes = emptyByte
						continue
					}

					if len(payloadBytes) != packetLength {
						log.Printf("⚠️  LENGTH MISMATCH: Expected %d bytes, got %d bytes", packetLength, len(payloadBytes))
						processedBytes = emptyByte
						continue
					}

					// Try to decode as protobuf first - if it fails, treat as text
					fromRadio := pb.FromRadio{}
					if err := proto.Unmarshal(payloadBytes, &fromRadio); err != nil {
						// Protobuf parsing failed - treat as text data
						log.Printf("🔍 PROTOBUF DECODE FAILED: Treating as text data (len=%d)", len(payloadBytes))
						textBuffer = append(textBuffer, processedBytes...)
						processedBytes = emptyByte
						continue
					}

					log.Printf("✅ PROTOBUF DECODED: Type=%T, PayloadVariant=%T", &fromRadio, fromRadio.PayloadVariant)

					responseSet.ProtobufPackets = append(responseSet.ProtobufPackets, &fromRadio)
					responseSet.AllResponses = append(responseSet.AllResponses, &RadioResponse{
						Type:        ResponseTypeProtobuf,
						ProtobufMsg: &fromRadio,
						RawBytes:    make([]byte, len(processedBytes)),
					})
					copy(responseSet.AllResponses[len(responseSet.AllResponses)-1].RawBytes, processedBytes)

					processedBytes = emptyByte
				}
			} else {
				// Not in protobuf sequence, accumulate as potential text data
				textBuffer = append(textBuffer, b...)

				// Reset protobuf processing if we were in the middle of it
				if len(processedBytes) > 0 {
					// Only log if we have significant data to avoid spam
					if len(processedBytes) > 4 {
						log.Printf("🔍 HEADER: Expected START1, got text data - resetting (%d bytes)", len(processedBytes))
					}
					textBuffer = append(textBuffer, processedBytes...)
					processedBytes = emptyByte
				}
			}
		} else {
			log.Printf("📥 READRESPONSE_ENHANCED: Empty byte received, breaking")
			break
		}
	}

	// Process any remaining text data
	if len(textBuffer) > 0 && isTextData(textBuffer) {
		textLines := extractTextFromBytes(textBuffer)
		if len(textLines) > 0 {
			// Completely suppress final text data logging to reduce noise
			for _, line := range textLines {
				responseSet.TextMessages = append(responseSet.TextMessages, line)
				responseSet.AllResponses = append(responseSet.AllResponses, &RadioResponse{
					Type:     ResponseTypeText,
					TextData: line,
					RawBytes: []byte(line),
				})
			}
		}
	}

	log.Printf("📥 READRESPONSE_ENHANCED: Completed - Found %d protobuf packets, %d text messages, TotalBytesRead=%d",
		len(responseSet.ProtobufPackets), len(responseSet.TextMessages), totalBytesRead)

	if len(processedBytes) > 0 {
		log.Printf("⚠️  READRESPONSE_ENHANCED: %d unprocessed protobuf bytes remaining: %x",
			len(processedBytes), processedBytes)
	}

	return responseSet, nil
}

// ReadResponse reads any responses in the serial port, convert them to a FromRadio protobuf and return
func (r *Radio) ReadResponse(timeout bool) (FromRadioPackets []*pb.FromRadio, err error) {
	log.Printf("📥 READRESPONSE: Starting to read radio response (timeout=%v)", timeout)

	b := make([]byte, 1)
	emptyByte := make([]byte, 0)
	processedBytes := make([]byte, 0)
	repeatByteCounter := 0
	previousByte := make([]byte, 1)
	totalBytesRead := 0

	/************************************************************************************************
	* Process the returned data byte by byte until we have a valid command
	* Each command will come back with [START1, START2, PROTOBUF_PACKET]
	* where the protobuf packet is sent in binary. After reading START1 and START2
	* we use the next bytes to find the length of the packet.
	* After finding the length the looop continues to gather bytes until the length of the gathered
	* bytes is equal to the packet length plus the header
	 */
	for {
		err := r.streamer.Read(b)
		if err == nil {
			totalBytesRead++
			// Suppress all byte-by-byte logging to reduce noise
		}

		if bytes.Equal(b, previousByte) {
			repeatByteCounter++
		} else {
			repeatByteCounter = 0
		}

		if err == io.EOF || repeatByteCounter > 20 || errors.Is(err, os.ErrDeadlineExceeded) {
			log.Printf("📥 READRESPONSE: Breaking loop - EOF=%v, RepeatCount=%d, Timeout=%v, TotalBytes=%d",
				err == io.EOF, repeatByteCounter, errors.Is(err, os.ErrDeadlineExceeded), totalBytesRead)
			break
		} else if err != nil {
			log.Printf("❌ READRESPONSE: Read error: %v", err)
			return nil, err
		}
		copy(previousByte, b)

		if len(b) > 0 {
			pointer := len(processedBytes)
			processedBytes = append(processedBytes, b...)

			if pointer == 0 {
				if b[0] != start1 {
					// Suppress logging completely for text data to reduce noise
					processedBytes = emptyByte
				} else {
					log.Printf("🔍 HEADER: Found START1 (0x%02x)", b[0])
				}
			} else if pointer == 1 {
				if b[0] != start2 {
					log.Printf("🔍 HEADER: Expected START2 (0x%02x), got 0x%02x - resetting", start2, b[0])
					processedBytes = emptyByte
				} else {
					log.Printf("🔍 HEADER: Found START2 (0x%02x)", b[0])
				}
			} else if pointer >= headerLen {
				packetLength := int((processedBytes[2] << 8) + processedBytes[3])

				if pointer == headerLen {
					log.Printf("🔍 PACKET LENGTH: Calculated length=%d (bytes 2-3: 0x%02x 0x%02x)",
						packetLength, processedBytes[2], processedBytes[3])
					if packetLength > maxToFromRadioSzie {
						log.Printf("❌ PACKET TOO LARGE: %d > %d - resetting", packetLength, maxToFromRadioSzie)
						processedBytes = emptyByte
					}
				}

				if len(processedBytes) != 0 && pointer+1 == packetLength+headerLen {
					payloadBytes := processedBytes[headerLen:]

					log.Printf("🔍 PARSING PROTOBUF: TotalLen=%d, HeaderLen=%d, PayloadLen=%d, ExpectedLen=%d",
						len(processedBytes), headerLen, len(payloadBytes), packetLength)

					// Validate payload before attempting to parse
					if len(payloadBytes) == 0 {
						log.Printf("⚠️  EMPTY PAYLOAD: Skipping empty protobuf payload")
						processedBytes = emptyByte
						continue
					}

					if len(payloadBytes) != packetLength {
						log.Printf("⚠️  LENGTH MISMATCH: Expected %d bytes, got %d bytes", packetLength, len(payloadBytes))
						processedBytes = emptyByte
						continue
					}

					// Try to decode as protobuf first - if it fails, skip (this function only returns protobuf)
					fromRadio := pb.FromRadio{}
					if err := proto.Unmarshal(payloadBytes, &fromRadio); err != nil {
						// Protobuf parsing failed - skip this data (ReadResponse only returns protobuf packets)
						log.Printf("🔍 PROTOBUF DECODE FAILED: Skipping non-protobuf data (len=%d)", len(payloadBytes))
						processedBytes = emptyByte
						continue
					}

					log.Printf("✅ PROTOBUF DECODED: Type=%T, PayloadVariant=%T",
						&fromRadio, fromRadio.PayloadVariant)

					FromRadioPackets = append(FromRadioPackets, &fromRadio)
					processedBytes = emptyByte
				}
			}

		} else {
			log.Printf("📥 READRESPONSE: Empty byte received, breaking")
			break
		}

	}

	log.Printf("📥 READRESPONSE: Completed - Found %d packets, TotalBytesRead=%d",
		len(FromRadioPackets), totalBytesRead)

	if len(processedBytes) > 0 {
		log.Printf("⚠️  READRESPONSE: %d unprocessed bytes remaining: %x",
			len(processedBytes), processedBytes)
	}

	return FromRadioPackets, nil

}

// ReadResponseBatch reads responses from the serial port with a maximum count limit
func (r *Radio) ReadResponseBatch(timeout bool, maxResponses int) (FromRadioPackets []*pb.FromRadio, err error) {
	log.Printf("📥 READRESPONSE_BATCH: Starting to read radio response (timeout=%v, maxResponses=%d)", timeout, maxResponses)

	b := make([]byte, 1)
	emptyByte := make([]byte, 0)
	processedBytes := make([]byte, 0)
	repeatByteCounter := 0
	previousByte := make([]byte, 1)
	totalBytesRead := 0
	responseCount := 0

	for responseCount < maxResponses {
		err := r.streamer.Read(b)
		if err == nil {
			totalBytesRead++
		}

		if bytes.Equal(b, previousByte) {
			repeatByteCounter++
		} else {
			repeatByteCounter = 0
		}

		if err == io.EOF || repeatByteCounter > 20 || errors.Is(err, os.ErrDeadlineExceeded) {
			log.Printf("📥 READRESPONSE_BATCH: Breaking loop - EOF=%v, RepeatCount=%d, Timeout=%v, TotalBytes=%d, Responses=%d",
				err == io.EOF, repeatByteCounter, errors.Is(err, os.ErrDeadlineExceeded), totalBytesRead, responseCount)
			break
		} else if err != nil {
			log.Printf("❌ READRESPONSE_BATCH: Read error: %v", err)
			return nil, err
		}
		copy(previousByte, b)

		if len(b) > 0 {
			pointer := len(processedBytes)
			processedBytes = append(processedBytes, b...)

			if pointer == 0 {
				if b[0] != start1 {
					// Suppress logging completely for text data to reduce noise
					processedBytes = emptyByte
				} else {
					log.Printf("🔍 HEADER: Found START1 (0x%02x)", b[0])
				}
			} else if pointer == 1 {
				if b[0] != start2 {
					log.Printf("⚠️  HEADER: Expected START2 (0x%02x) but got (0x%02x), resetting", start2, b[0])
					processedBytes = emptyByte
				} else {
					log.Printf("🔍 HEADER: Found START2 (0x%02x)", b[0])
				}
			} else if pointer == 2 || pointer == 3 {
				// Length bytes - continue collecting
			} else if pointer >= 4 {
				// We have header, now check if we have complete packet
				if len(processedBytes) >= 4 {
					packetLength := int((processedBytes[2] << 8) + processedBytes[3]) // Big-endian like other functions
					totalExpectedLength := 4 + packetLength

					if len(processedBytes) >= totalExpectedLength {
						// We have a complete packet
						log.Printf("🔍 PACKET LENGTH: Calculated length=%d (bytes 2-3: 0x%02x 0x%02x)",
							packetLength, processedBytes[2], processedBytes[3])

						payloadBytes := processedBytes[4:totalExpectedLength]

						log.Printf("🔍 PARSING PROTOBUF: TotalLen=%d, HeaderLen=4, PayloadLen=%d, ExpectedLen=%d",
							len(processedBytes), len(payloadBytes), packetLength)

						// Try to decode as protobuf first - if it fails, skip (this function only returns protobuf)
						var fromRadio pb.FromRadio
						if err := proto.Unmarshal(payloadBytes, &fromRadio); err != nil {
							log.Printf("🔍 PROTOBUF DECODE FAILED: Skipping non-protobuf data (len=%d)", len(payloadBytes))
							processedBytes = emptyByte
							continue
						}

						log.Printf("✅ PROTOBUF DECODED: Type=%T, PayloadVariant=%T", &fromRadio, fromRadio.PayloadVariant)
						FromRadioPackets = append(FromRadioPackets, &fromRadio)
						responseCount++

						// Remove processed packet and continue with remaining bytes
						if len(processedBytes) > totalExpectedLength {
							processedBytes = processedBytes[totalExpectedLength:]
						} else {
							processedBytes = emptyByte
						}

						// Check if we've reached our limit
						if responseCount >= maxResponses {
							log.Printf("📥 READRESPONSE_BATCH: Reached max responses limit (%d), stopping", maxResponses)
							break
						}
					}
				}
			}

		} else {
			log.Printf("📥 READRESPONSE_BATCH: Empty byte received, breaking")
			break
		}
	}

	log.Printf("📥 READRESPONSE_BATCH: Completed - Found %d packets, TotalBytesRead=%d",
		len(FromRadioPackets), totalBytesRead)

	if len(processedBytes) > 0 {
		log.Printf("⚠️  READRESPONSE_BATCH: %d unprocessed bytes remaining: %x",
			len(processedBytes), processedBytes)
	}

	return FromRadioPackets, nil
}

// ReadTextResponse reads text responses from the serial port, filtering out protobuf data
func (r *Radio) ReadTextResponse(timeout bool) ([]string, error) {
	responseSet, err := r.ReadResponseWithTypes(timeout)
	if err != nil {
		return nil, err
	}
	return responseSet.TextMessages, nil
}

// ReadProtobufResponse reads protobuf responses from the serial port, filtering out text data
func (r *Radio) ReadProtobufResponse(timeout bool) ([]*pb.FromRadio, error) {
	responseSet, err := r.ReadResponseWithTypes(timeout)
	if err != nil {
		return nil, err
	}
	return responseSet.ProtobufPackets, nil
}

// createAdminPacket builds a admin message packet to send to the radio
func (r *Radio) createAdminPacket(nodeNum uint32, payload []byte) (packetOut []byte, err error) {

	radioMessage := pb.ToRadio{
		PayloadVariant: &pb.ToRadio_Packet{
			Packet: &pb.MeshPacket{
				To:      nodeNum,
				WantAck: true,
				PayloadVariant: &pb.MeshPacket_Decoded{
					Decoded: &pb.Data{
						Payload:      payload,
						Portnum:      pb.PortNum_ADMIN_APP,
						WantResponse: true,
					},
				},
			},
		},
	}

	packetOut, err = proto.Marshal(&radioMessage)
	if err != nil {
		return nil, err
	}

	return

}

// getNodeNum returns the current NodeNumber after querying the radio
func (r *Radio) getNodeNum() (err error) {
	// Send first request for Radio and Node information
	nodeInfo := pb.ToRadio{PayloadVariant: &pb.ToRadio_WantConfigId{WantConfigId: 42}}

	out, err := proto.Marshal(&nodeInfo)
	if err != nil {
		return err
	}

	r.sendPacket(out)

	radioResponses, err := r.GetRadioInfo()
	if err != nil {
		return err
	}

	// Gather the Node number for channel settings requests
	nodeNum := uint32(0)
	myInfoCount := 0
	for _, response := range radioResponses {
		if info, ok := response.GetPayloadVariant().(*pb.FromRadio_MyInfo); ok {
			nodeNum = info.MyInfo.MyNodeNum
			myInfoCount++
			log.Printf("🎯 FOUND MyInfo PACKET: NodeNum=%d (!%x)", nodeNum, nodeNum)
		}
	}

	log.Printf("📊 NODE ID DETECTION: Found %d MyInfo packets out of %d total responses", myInfoCount, len(radioResponses))
	if nodeNum == 0 {
		log.Printf("⚠️  NO MyInfo PACKET FOUND - Node ID will be 0")
	}

	r.nodeNum = nodeNum
	return
}

// GetRadioInfo retrieves information from the radio including config and adjacent Node information
func (r *Radio) GetRadioInfo() (radioResponses []*pb.FromRadio, err error) {
	log.Printf("🔄 GETRADIOINFO: Starting radio info request")

	// Send first request for Radio and Node information
	nodeInfo := pb.ToRadio{PayloadVariant: &pb.ToRadio_WantConfigId{WantConfigId: 42}}

	out, err := proto.Marshal(&nodeInfo)
	if err != nil {
		log.Printf("❌ GETRADIOINFO: Failed to marshal WantConfigId: %v", err)
		return nil, err
	}

	err = r.sendPacket(out)
	if err != nil {
		return nil, err
	}

	checks := 0

	log.Printf("📥 GETRADIOINFO: Reading initial response...")
	radioResponses, err = r.ReadResponse(true)

	if err != nil {
		log.Printf("❌ GETRADIOINFO: Initial ReadResponse failed: %v", err)
		return nil, err
	}

	log.Printf("📊 GETRADIOINFO: Initial response count: %d", len(radioResponses))
	for i, resp := range radioResponses {
		log.Printf("📊 GETRADIOINFO: Response #%d: Type=%T, PayloadVariant=%T",
			i, resp, resp.PayloadVariant)
	}

	for checks < 5 && len(radioResponses) == 0 {
		log.Printf("🔄 GETRADIOINFO: Retry %d/5 - no responses yet", checks+1)

		// Add a small delay before retry to let radio process
		time.Sleep(500 * time.Millisecond)

		radioResponses, err = r.ReadResponse(true)
		if err != nil {
			log.Printf("❌ GETRADIOINFO: ReadResponse retry %d failed: %v", checks+1, err)
			// Don't return immediately - try a few more times
			checks++
			time.Sleep(1 * time.Second)
			continue
		}

		log.Printf("📊 GETRADIOINFO: Retry %d response count: %d", checks+1, len(radioResponses))
		for i, resp := range radioResponses {
			log.Printf("📊 GETRADIOINFO: Retry %d Response #%d: Type=%T, PayloadVariant=%T",
				checks+1, i, resp, resp.PayloadVariant)
		}

		checks++
		if len(radioResponses) == 0 {
			time.Sleep(1 * time.Second)
		}
	}

	if len(radioResponses) == 0 {
		log.Printf("❌ GETRADIOINFO: No responses after %d retries", checks)
		return nil, errors.New("failed to get radio info after multiple retries")
	}

	log.Printf("✅ GETRADIOINFO: Success! Got %d responses after %d attempts", len(radioResponses), checks+1)
	return

}

// SendTextMessage sends a free form text message to other radios
func (r *Radio) SendTextMessage(message string, to int64, channel int64) error {
	var address int64
	if to == 0 {
		address = broadcastNum
	} else {
		address = to
	}

	// This constant is defined in Constants_DATA_PAYLOAD_LEN, but not in a friendly way to use
	if len(message) > 240 {
		return errors.New("message too large")
	}

	rand.Seed(time.Now().UnixNano())
	packetID := rand.Intn(2386828-1) + 1

	radioMessage := pb.ToRadio{
		PayloadVariant: &pb.ToRadio_Packet{
			Packet: &pb.MeshPacket{
				To:      uint32(address),
				WantAck: true,
				Id:      uint32(packetID),
				Channel: uint32(channel),
				PayloadVariant: &pb.MeshPacket_Decoded{
					Decoded: &pb.Data{
						Payload: []byte(message),
						Portnum: pb.PortNum_TEXT_MESSAGE_APP,
					},
				},
			},
		},
	}

	out, err := proto.Marshal(&radioMessage)
	if err != nil {
		return err
	}

	if err := r.sendPacket(out); err != nil {
		return err
	}

	return nil

}

// SetRadioOwner sets the owner of the radio visible on the public mesh
func (r *Radio) SetRadioOwner(name string) error {

	if len(name) <= 2 {
		return errors.New("name too short")
	}

	adminPacket := pb.AdminMessage{
		PayloadVariant: &pb.AdminMessage_SetOwner{
			SetOwner: &pb.User{
				LongName:  name,
				ShortName: name[:3],
			},
		},
	}

	out, err := proto.Marshal(&adminPacket)
	if err != nil {
		return err
	}

	nodeNum := r.nodeNum

	packet, err := r.createAdminPacket(nodeNum, out)
	if err != nil {
		return err
	}

	if err := r.sendPacket(packet); err != nil {
		return err
	}

	return nil
}

// SetModemMode sets the channel modem setting to be fast or slow
func (r *Radio) SetModemMode(mode string) error {

	var modemSetting pb.Config_LoRaConfig_ModemPreset

	if mode == "lf" {
		modemSetting = pb.Config_LoRaConfig_LONG_FAST
	} else if mode == "ls" {
		modemSetting = pb.Config_LoRaConfig_LONG_SLOW
	} else if mode == "vls" {
		modemSetting = pb.Config_LoRaConfig_VERY_LONG_SLOW
	} else if mode == "ms" {
		modemSetting = pb.Config_LoRaConfig_MEDIUM_SLOW
	} else if mode == "mf" {
		modemSetting = pb.Config_LoRaConfig_MEDIUM_FAST
	} else if mode == "sl" {
		modemSetting = pb.Config_LoRaConfig_SHORT_SLOW
	} else if mode == "sf" {
		modemSetting = pb.Config_LoRaConfig_SHORT_FAST
	} else if mode == "lm" {
		modemSetting = pb.Config_LoRaConfig_LONG_MODERATE
	}

	adminPacket := pb.AdminMessage{
		PayloadVariant: &pb.AdminMessage_SetConfig{
			SetConfig: &pb.Config{
				PayloadVariant: &pb.Config_Lora{
					Lora: &pb.Config_LoRaConfig{
						ModemPreset: modemSetting,
					},
				},
			},
		},
	}

	out, err := proto.Marshal(&adminPacket)
	if err != nil {
		return err
	}

	nodeNum := r.nodeNum

	packet, err := r.createAdminPacket(nodeNum, out)
	if err != nil {
		return err
	}

	if err := r.sendPacket(packet); err != nil {
		return err
	}

	return nil

}

// SetLocation sets a fixed location for the radio
func (r *Radio) SetLocation(lat int32, long int32, alt int32) error {

	positionPacket := pb.Position{
		LatitudeI:  &lat,
		LongitudeI: &long,
		Altitude:   &alt,
	}

	out, err := proto.Marshal(&positionPacket)
	if err != nil {
		return err
	}

	nodeNum := r.nodeNum

	radioMessage := pb.ToRadio{
		PayloadVariant: &pb.ToRadio_Packet{
			Packet: &pb.MeshPacket{
				To:      nodeNum,
				WantAck: true,
				PayloadVariant: &pb.MeshPacket_Decoded{
					Decoded: &pb.Data{
						Payload:      out,
						Portnum:      pb.PortNum_POSITION_APP,
						WantResponse: true,
					},
				},
			},
		},
	}

	packet, err := proto.Marshal(&radioMessage)
	if err != nil {
		return err
	}

	if err := r.sendPacket(packet); err != nil {
		return err
	}

	return nil
}

// SetNodeFavorite marks a node as favorite on the radio device
func (r *Radio) SetNodeFavorite(nodeID uint32) error {
	log.Printf("🌟 GOMESH: SetNodeFavorite called for node %d (!%x)", nodeID, nodeID)

	adminPacket := pb.AdminMessage{
		PayloadVariant: &pb.AdminMessage_SetFavoriteNode{
			SetFavoriteNode: nodeID,
		},
	}

	out, err := proto.Marshal(&adminPacket)
	if err != nil {
		log.Printf("❌ GOMESH: Failed to marshal admin packet: %v", err)
		return err
	}

	log.Printf("✅ GOMESH: Admin packet marshaled successfully, size: %d bytes", len(out))

	nodeNum := r.nodeNum
	log.Printf("🔍 GOMESH: Using nodeNum %d for admin packet", nodeNum)

	packet, err := r.createAdminPacket(nodeNum, out)
	if err != nil {
		log.Printf("❌ GOMESH: Failed to create admin packet: %v", err)
		return err
	}

	log.Printf("✅ GOMESH: Admin packet created successfully, size: %d bytes", len(packet))

	if err := r.sendPacket(packet); err != nil {
		log.Printf("❌ GOMESH: Failed to send packet: %v", err)
		return err
	}

	log.Printf("✅ GOMESH: SetNodeFavorite packet sent successfully for node %d", nodeID)
	return nil
}

// RemoveNodeFavorite removes a node from favorites on the radio device
func (r *Radio) RemoveNodeFavorite(nodeID uint32) error {
	log.Printf("🌟 GOMESH: RemoveNodeFavorite called for node %d (!%x)", nodeID, nodeID)

	adminPacket := pb.AdminMessage{
		PayloadVariant: &pb.AdminMessage_RemoveFavoriteNode{
			RemoveFavoriteNode: nodeID,
		},
	}

	out, err := proto.Marshal(&adminPacket)
	if err != nil {
		log.Printf("❌ GOMESH: Failed to marshal admin packet: %v", err)
		return err
	}

	log.Printf("✅ GOMESH: Admin packet marshaled successfully, size: %d bytes", len(out))

	nodeNum := r.nodeNum
	log.Printf("🔍 GOMESH: Using nodeNum %d for admin packet", nodeNum)

	packet, err := r.createAdminPacket(nodeNum, out)
	if err != nil {
		log.Printf("❌ GOMESH: Failed to create admin packet: %v", err)
		return err
	}

	log.Printf("✅ GOMESH: Admin packet created successfully, size: %d bytes", len(packet))

	if err := r.sendPacket(packet); err != nil {
		log.Printf("❌ GOMESH: Failed to send packet: %v", err)
		return err
	}

	log.Printf("✅ GOMESH: RemoveNodeFavorite packet sent successfully for node %d", nodeID)
	return nil
}

// Send a factory reset command to the radio
func (r *Radio) FactoryRest() error {
	adminPacket := pb.AdminMessage{
		PayloadVariant: &pb.AdminMessage_FactoryResetDevice{
			FactoryResetDevice: 1,
		},
	}
	out, err := proto.Marshal(&adminPacket)
	if err != nil {
		return err
	}

	nodeNum := r.nodeNum

	packet, err := r.createAdminPacket(nodeNum, out)
	if err != nil {
		return err
	}

	if err := r.sendPacket(packet); err != nil {
		return err
	}

	return nil
}

// Close closes the serial port. Added so users can defer the close after opening
func (r *Radio) Close() {
	r.streamer.Close()
}
