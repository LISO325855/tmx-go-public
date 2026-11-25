package whatsapp

// Message and composer limits.
const (
	// MaxAgentPending: the former limit on an agent's messages waiting for the owner's Allow.
	// Nothing is refused for it any more (the owner, 17 Nov: no talk limits).
	MaxAgentPending = 5
	// MaxTextBytes is the longest text tmx sends (WhatsApp takes more; people do not write more).
	MaxTextBytes = 16 << 10
	// MaxAgentTextBytes: an agent writes as much as the owner (4 KB until 17 Nov).
	MaxAgentTextBytes = MaxTextBytes
	// MaxUploadBytes per file and MaxUploads per request (the composer's limits).
	MaxUploadBytes = 16 << 20
	MaxUploads     = 5
)
