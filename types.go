package evpanda

// Protocol is the wire protocol a Client serves, set via
// Config.NetworkType.
type Protocol string

const (
	// ProtocolOCPI is OCPI roaming HTTP traffic.
	ProtocolOCPI Protocol = "ocpi"
	// ProtocolOCPP is OCPP charger WebSocket traffic.
	ProtocolOCPP Protocol = "ocpp"
)

// Direction is a message's direction relative to the host.
type Direction string

const (
	// Inbound is traffic received by the host.
	Inbound Direction = "inbound"
	// Outbound is traffic sent by the host.
	Outbound Direction = "outbound"
)

// OCPPEventType is an OCPP WebSocket lifecycle event.
type OCPPEventType int

const (
	// OCPPEventTypeDisconnect indicates the WebSocket closed.
	OCPPEventTypeDisconnect OCPPEventType = 0
	// OCPPEventTypeConnect indicates the WebSocket was established.
	OCPPEventTypeConnect OCPPEventType = 1
	// OCPPEventTypeMessage indicates a message frame.
	OCPPEventTypeMessage OCPPEventType = 2
)

// CapturedHTTP is a captured HTTP exchange. The caller is responsible for
// truncating bodies to Config.MaxCaptureBytes.
type CapturedHTTP struct {
	Method          string
	URL             string
	StatusCode      int
	RequestHeaders  map[string]string
	ResponseHeaders map[string]string
	RequestBody     []byte
	ResponseBody    []byte
}

// OCPIMessage is a captured OCPI HTTP message. Pass it to Client.CaptureOCPI.
type OCPIMessage struct {
	Direction Direction
	Identity  RoamingIdentity
	HTTP      CapturedHTTP
}

// OCPPMessage is a captured OCPP WebSocket event. Pass it to
// Client.CaptureOCPP.
type OCPPMessage struct {
	EventType OCPPEventType
	Identity  ChargerIdentity
	// ConnectionID is a caller-supplied identifier, stable for the life of
	// a connection.
	ConnectionID string
	// Direction is optional for OCPP.
	Direction Direction
	Payload   []byte
}

// anyMessage is an OCPIMessage or OCPPMessage; it lets the buffer hold
// either without a protocol tag.
type anyMessage interface {
	isMessage()
}

func (OCPIMessage) isMessage() {}
func (OCPPMessage) isMessage() {}
