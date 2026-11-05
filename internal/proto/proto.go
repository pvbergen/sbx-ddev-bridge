// Package proto defines the wire format between the sandbox client and the host bridge.
//
// The client POSTs a RunRequest to RunPath with "Authorization: Bearer <token>".
// The bridge answers 200 with a stream of newline-delimited JSON Frames:
// zero or more output frames, then exactly one final frame carrying Exit.
package proto

// RunPath is the HTTP endpoint the client posts to. v2 = streaming NDJSON.
const RunPath = "/v2/run"

// ContentType of the streamed response.
const ContentType = "application/x-ndjson"

// ExitDenied is returned when the policy rejects a command (like "command cannot execute").
const ExitDenied = 126

// ExitBridgeError is returned by the client when the bridge cannot be reached or misbehaves.
const ExitBridgeError = 125

// RunRequest is the request body. Argv excludes the leading "ddev",
// e.g. ["drush", "cr"] or ["logs", "-s", "web"].
type RunRequest struct {
	Argv []string `json:"argv"`
}

// Stream names used in output frames.
const (
	Stdout = "out"
	Stderr = "err"
)

// Frame is one line of the response stream.
type Frame struct {
	Stream string `json:"s,omitempty"` // Stdout or Stderr, for output frames
	Data   []byte `json:"b,omitempty"` // output bytes (base64 in JSON)
	Exit   *int   `json:"exit,omitempty"`
	Denied string `json:"denied,omitempty"` // set on the final frame when the policy refused
}
