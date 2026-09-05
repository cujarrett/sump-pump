// Package event is the contract between the two binaries - the bridge publishes
// these subjects, the consumer reads them. Both import it so a subject or field
// rename cannot land on one side only.
package event

const (
	// Stream is the JetStream stream the Topic XR creates, its name derived
	// from the topic name in homelab-workspaces.
	Stream = "HOME-APPLIANCES"

	SubjectRunning = "home.appliance.sump-pump.running"
	SubjectIdle    = "home.appliance.sump-pump.idle"
)

// Reading is the JSON body published on both subjects.
type Reading struct {
	Watts float64 `json:"watts"`
}
