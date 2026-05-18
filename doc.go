// Package evpanda provides passive OCPI/OCPP traffic capture for embedding
// in OCPI servers and OCPP CSMS. It records protocol messages, buffers them
// in-process, and ships them in batches to the EVPanda ingestion API.
//
// The SDK stays out of the host's way: capture calls are non-blocking and
// never panic, memory is bounded, and under stress or network failure it
// drops data rather than degrading the application.
//
// Capture via the [Client] returned by [Start]:
//
//	// APIKey omitted ⇒ read from the EVPANDA_API_KEY env var.
//	panda, err := evpanda.Start(evpanda.Config{
//		NetworkType: evpanda.ProtocolOCPI,
//		Endpoint:    "https://ingest.evpanda.io",
//	})
//	if err != nil {
//		log.Printf("evpanda: %v (running inert)", err)
//	}
//	defer panda.Close()
//
//	panda.CaptureOCPI(ctx, evpanda.OCPIMessage{ /* ... */ })
//
// One Client serves a single protocol (Config.NetworkType); the other
// Capture method is a no-op.
package evpanda
