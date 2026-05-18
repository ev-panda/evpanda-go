// Port of src/types.ts. Hand-maintained message types; must match
// apispec/ingestion-api.yaml (fixture pack). `protocol` is Client-wide
// (Config.Protocol); `capturedAt` is the SDK-owned per-message receive
// time (internal envelope, see buffer.go) — neither lives on these types.

package evpanda

// Protocol identifies the captured wire protocol. One Client serves a
// single protocol (set via Config.Protocol).
type Protocol string

const (
	// ProtocolOCPI — OCPI roaming HTTP traffic.
	ProtocolOCPI Protocol = "ocpi"
	// ProtocolOCPP — OCPP charger WebSocket traffic.
	ProtocolOCPP Protocol = "ocpp"
)

// Direction is the message direction relative to the host.
type Direction string

const (
	// Inbound — received by the host.
	Inbound Direction = "inbound"
	// Outbound — sent by the host.
	Outbound Direction = "outbound"
)

// OCPPEventType maps an OCPP WS lifecycle event to the ingestion
// event_type. Numeric, matching the Node enum.
type OCPPEventType int

const (
	// OCPPEventTypeDisconnect — WebSocket closed.
	OCPPEventTypeDisconnect OCPPEventType = 0
	// OCPPEventTypeConnect — WebSocket established.
	OCPPEventTypeConnect OCPPEventType = 1
	// OCPPEventTypeMessage — a message frame.
	OCPPEventTypeMessage OCPPEventType = 2
)

// CapturedHTTP is a captured HTTP exchange. Body fields are []byte and
// marshal to base64 in JSON (matching the Node Uint8Array→base64 wire
// shape). Bodies are expected pre-truncated to MaxCaptureBytes by the
// caller (the Node adapters did this; adapters are not ported).
type CapturedHTTP struct {
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	StatusCode      int               `json:"statusCode,omitempty"`
	RequestHeaders  map[string]string `json:"requestHeaders"`
	ResponseHeaders map[string]string `json:"responseHeaders"`
	RequestBody     []byte            `json:"requestBody,omitempty"`
	ResponseBody    []byte            `json:"responseBody,omitempty"`
	Truncated       bool              `json:"truncated"`
}

// OCPIMessage is a captured OCPI HTTP message.
type OCPIMessage struct {
	Direction Direction       `json:"direction"`
	Identity  RoamingIdentity `json:"identity"`
	HTTP      CapturedHTTP    `json:"http"`
}

// OCPPMessage is a captured OCPP WebSocket event.
type OCPPMessage struct {
	EventType OCPPEventType   `json:"eventType"`
	Identity  ChargerIdentity `json:"identity"`
	// ConnectionID is an SDK-owned UUID, stable per connection, regenerated
	// on reconnect (supplied by the caller).
	ConnectionID string `json:"connectionId"`
	Payload      []byte `json:"payload,omitempty"`
	Truncated    bool   `json:"truncated"`
}

// anyMessage is either captured message shape — used where the protocol is
// not statically known (buffer/transport). Unexported: not a customer type.
type anyMessage interface {
	isMessage()
}

func (OCPIMessage) isMessage() {}
func (OCPPMessage) isMessage() {}
