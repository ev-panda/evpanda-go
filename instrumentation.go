package evpanda

import "strings"

// captureOCPI validates, redacts, and buffers an OCPI message. Messages
// with an invalid identity are dropped.
func captureOCPI(buf *ringBuffer, msg OCPIMessage) {
	capturedAt := nowISO()
	if !validateRoamingIdentity(msg.Identity) {
		return
	}
	buf.enqueue(bufferedMessage{capturedAt: capturedAt, message: redact(msg)})
}

// captureOCPP validates and buffers an OCPP message. Messages with an
// invalid identity are dropped.
func captureOCPP(buf *ringBuffer, msg OCPPMessage) {
	capturedAt := nowISO()
	if !validateChargerIdentity(msg.Identity) {
		return
	}
	buf.enqueue(bufferedMessage{capturedAt: capturedAt, message: redact(msg)})
}

// defaultHeaderDenylist is the set of headers stripped before buffering,
// matched case-insensitively.
var defaultHeaderDenylist = map[string]struct{}{
	"authorization": {},
	"x-api-key":     {},
	"cookie":        {},
}

// stripHeaders returns a copy of h without the denylisted headers. The
// result is always non-nil.
func stripHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if _, denied := defaultHeaderDenylist[strings.ToLower(k)]; !denied {
			out[k] = v
		}
	}
	return out
}

// redact returns msg with denylisted HTTP headers removed. It does not
// mutate the input; OCPP messages are returned unchanged.
func redact(msg anyMessage) anyMessage {
	m, ok := msg.(OCPIMessage)
	if !ok {
		return msg
	}
	m.HTTP.RequestHeaders = stripHeaders(m.HTTP.RequestHeaders)
	m.HTTP.ResponseHeaders = stripHeaders(m.HTTP.ResponseHeaders)
	return m
}
