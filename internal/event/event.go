// Package event is an in-process pub/sub event bus with a ring buffer.
package event

import "time"

// Kind enumerates the well-known event kinds. New kinds may be added freely;
// consumers should treat unknown kinds as informational.
const (
	KindMCPRequest       = "mcp.request"
	KindMCPResponse      = "mcp.response"
	KindChildAttached    = "child.attached"
	KindChildCrashed     = "child.crashed"
	KindChildRestarted   = "child.restarted"
	KindChildDisabled    = "child.disabled"
	KindConfigReload     = "config.reload"
	KindToolsChanged     = "tools.changed"
	KindResourcesChanged = "resources.changed"
	KindPromptsChanged   = "prompts.changed"
)

// Event is a single bus message. All fields are optional except Time and Kind.
type Event struct {
	Time     time.Time      `json:"time"`
	Kind     string         `json:"kind"`
	Server   string         `json:"server,omitempty"`   // server prefix, if applicable
	Method   string         `json:"method,omitempty"`   // for mcp.* events
	Duration time.Duration  `json:"duration,omitempty"`
	Bytes    int            `json:"bytes,omitempty"`
	Error    string         `json:"error,omitempty"`
	Extra    map[string]any `json:"extra,omitempty"`
}
