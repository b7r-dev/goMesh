# GoMesh Log Levels

The gomesh package now supports configurable log levels to control the verbosity of debug output.

## Available Log Levels

- **LogLevelSilent** (0): No debug logs at all
- **LogLevelError** (1): Only error messages
- **LogLevelWarn** (2): Errors and warnings
- **LogLevelInfo** (3): Errors, warnings, and info messages (default)
- **LogLevelDebug** (4): All logs including detailed debug information

## Usage

### Setting the Log Level

```go
import "github.com/b7r-dev/goMesh"

func main() {
    // Set to warning level to reduce verbose output
    gomesh.SetLogLevel(gomesh.LogLevelWarn)
    
    // Now use gomesh as normal
    radio := &gomesh.Radio{}
    err := radio.Init("/dev/tty.usbserial-0001")
    // ...
}
```

### Recommended Settings

- **Production**: `LogLevelWarn` - Only shows errors and warnings
- **Development**: `LogLevelInfo` - Shows important events and errors
- **Debugging**: `LogLevelDebug` - Shows all debug information including packet parsing details

## What Gets Logged at Each Level

### Error Level
- Failed to marshal/unmarshal packets
- Failed to send packets
- Read errors from serial port
- Failed to switch to API mode

### Warn Level
- Empty payloads
- Length mismatches
- Unprocessed bytes remaining
- No MyInfo packet found

### Info Level
- Radio initialization
- Radio info requests
- Packet counts and statistics
- Node ID detection
- Successful operations

### Debug Level
- Individual packet header detection
- Packet length calculations
- Protobuf parsing details
- Packet encoding/decoding details
- All lower level operations

## Example: Reducing Verbose Output

Before (with default LogLevelInfo):
```
2025/10/16 10:02:50 🔍 PARSING PROTOBUF: TotalLen=159, HeaderLen=4, PayloadLen=155, ExpectedLen=155
2025/10/16 10:02:50 ✅ PROTOBUF DECODED: Type=*generated.FromRadio, PayloadVariant=*generated.FromRadio_NodeInfo
2025/10/16 10:02:50 🔍 HEADER: Found START1 (0x94)
2025/10/16 10:02:50 🔍 HEADER: Found START2 (0xc3)
... (thousands of similar lines)
```

After (with LogLevelWarn):
```
2025/10/16 10:02:50 ✅ RADIO INIT: Successfully switched to API mode
2025/10/16 10:02:50 📊 NODE ID DETECTION: Found 1 MyInfo packets out of 42 total responses
2025/10/16 10:02:50 ✅ GETRADIOINFO: Success! Got 42 responses after 1 attempts
```

Much cleaner!

