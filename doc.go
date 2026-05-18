// Package evpanda is the Go port of @evpanda/sdk: passive OCPI / OCPP
// traffic capture for embedding in customer OCPI servers and OCPP CSMS.
//
// Embed it in your OCPI server or OCPP CSMS; it records protocol messages,
// buffers them in-process, and ships them in batches to the EVPanda
// ingestion API.
//
// It never gets in your way. The SDK will not block your request path,
// panic into your handlers, crash your process, or grow memory unbounded.
// Under stress or with the network down it drops data — it never degrades
// your application.
//
// Capture via the *Client returned by Start:
//
//	// APIKey omitted ⇒ read from the EVPANDA_API_KEY env var.
//	sdk, err := evpanda.Start(evpanda.Config{
//		NetworkType: evpanda.ProtocolOCPI,
//		Endpoint:    "https://ingest.evpanda.io",
//	})
//	if err != nil {
//		log.Printf("evpanda: %v (running inert)", err)
//	}
//	defer sdk.Close()
//
//	sdk.CaptureOCPI(evpanda.OCPIMessage{ /* ... */ })
//	sdk.CaptureOCPP(evpanda.OCPPMessage{ /* ... */ })
//
// This package mirrors the structure of the Node SDK one file at a time
// (config, types, identity, buffer, instrumentation, transport, worker,
// sdk). The framework adapters from the Node SDK are intentionally not
// ported — only the core capture pipeline.
package evpanda
