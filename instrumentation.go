// Port of src/instrumentation.ts. The two stable capture primitives — the
// single chokepoint. Order: stamp protocol+capturedAt → validate identity
// (invalid ⇒ drop) → redact → enqueue. May panic on pathological input;
// the Client facade isolates it (recover).

package evpanda

import "strings"

func captureOCPI(buf *ringBuffer, msg OCPIMessage) {
	capturedAt := nowISO()
	if !validateRoamingIdentity(msg.Identity) {
		return // drop, never panic
	}
	buf.enqueue(bufferedMessage{
		capturedAt: capturedAt,
		message:    redact(msg),
	})
}

func captureOCPP(buf *ringBuffer, msg OCPPMessage) {
	capturedAt := nowISO()
	if !validateChargerIdentity(msg.Identity) {
		return // drop, never panic
	}
	buf.enqueue(bufferedMessage{
		capturedAt: capturedAt,
		message:    redact(msg),
	})
}

// ── Redaction (internal, always-on header denylist; no customer hook) ────

// defaultHeaderDenylist is always stripped (lowercase; matched
// case-insensitively).
var defaultHeaderDenylist = map[string]struct{}{
	"authorization": {},
	"x-api-key":     {},
	"cookie":        {},
}

// stripHeaders copies headers minus the denylisted ones (case-insensitive).
// Always returns a non-nil map so the JSON shape is {} (not null), matching
// the Node SDK.
func stripHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if _, denied := defaultHeaderDenylist[strings.ToLower(k)]; !denied {
			out[k] = v
		}
	}
	return out
}

// redact strips denylisted headers. Non-mutating (returns a copy); OCPP is
// unchanged (nothing to strip).
func redact(msg anyMessage) anyMessage {
	m, ok := msg.(OCPIMessage)
	if !ok {
		return msg // OCPP: nothing to strip
	}
	m.HTTP.RequestHeaders = stripHeaders(m.HTTP.RequestHeaders)
	m.HTTP.ResponseHeaders = stripHeaders(m.HTTP.ResponseHeaders)
	return m
}
