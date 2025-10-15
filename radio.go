package gomesh

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"time"

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
	log.Printf("🧹 SWITCHING TO API MODE: Clearing console buffer...")
	for i := 0; i < 10; i++ {
		b := make([]byte, 1024)
		err := r.streamer.Read(b)
		if err != nil {
			// If we get a timeout or EOF, that's expected - buffer is clear
			break
		}
		// Log what we're clearing (for debugging)
		if len(b) > 0 {
			log.Printf("🧹 CLEARED CONSOLE DATA: %q", string(b[:min(100, len(b))]))
		}
		time.Sleep(50 * time.Millisecond)
	}

	log.Printf("✅ SWITCHING TO API MODE: Mode switch complete")
	return nil
}

// sendPacket takes a protbuf packet, construct the appropriate header and sends it to the radio
func (r *Radio) sendPacket(protobufPacket []byte) (err error) {

	packageLength := len(protobufPacket) // FIXED: Don't convert to string, which corrupts binary data

	header := []byte{start1, start2, byte(packageLength>>8) & 0xff, byte(packageLength) & 0xff}

	radioPacket := append(header, protobufPacket...)

	// Add debugging for packet sending
	log.Printf("📤 SENDING PACKET: ProtobufLen=%d, HeaderLen=%d, TotalLen=%d",
		len(protobufPacket), len(header), len(radioPacket))
	log.Printf("📤 PACKET HEADER: %x", header)
	log.Printf("📤 PROTOBUF DATA (first 32 bytes): %x", protobufPacket[:min(32, len(protobufPacket))])

	err = r.streamer.Write(radioPacket)
	if err != nil {
		log.Printf("❌ PACKET SEND FAILED: %v", err)
		return err
	}

	log.Printf("✅ PACKET SENT SUCCESSFULLY")
	return

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
			// Log every 100 bytes or when we get interesting bytes
			if totalBytesRead%100 == 0 || b[0] == start1 || b[0] == start2 {
				log.Printf("📥 BYTE READ #%d: 0x%02x (%d)", totalBytesRead, b[0], b[0])
			}
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
					log.Printf("🔍 HEADER: Expected START1 (0x%02x), got 0x%02x - resetting", start1, b[0])
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
					fromRadio := pb.FromRadio{}
					payloadBytes := processedBytes[headerLen:]

					log.Printf("🔍 PARSING PROTOBUF: TotalLen=%d, HeaderLen=%d, PayloadLen=%d, ExpectedLen=%d",
						len(processedBytes), headerLen, len(payloadBytes), packetLength)
					log.Printf("🔍 FULL PACKET: %x", processedBytes)
					log.Printf("🔍 PAYLOAD BYTES: %x", payloadBytes)

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

					if err := proto.Unmarshal(payloadBytes, &fromRadio); err != nil {
						log.Printf("❌ PROTOBUF PARSE ERROR: %v", err)
						log.Printf("❌ FAILED PAYLOAD (len=%d): %x", len(payloadBytes), payloadBytes)
						log.Printf("❌ FULL PACKET CONTEXT: %x", processedBytes)

						// Try to recover by looking for the next valid packet start
						log.Printf("🔄 ATTEMPTING RECOVERY: Looking for next packet start...")
						processedBytes = emptyByte
						continue
					}

					log.Printf("✅ PROTOBUF PARSED: Type=%T, PayloadVariant=%T",
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

	log.Printf("📤 GETRADIOINFO: Sending WantConfigId request (42), payload size: %d", len(out))
	err = r.sendPacket(out)
	if err != nil {
		log.Printf("❌ GETRADIOINFO: Failed to send WantConfigId packet: %v", err)
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
