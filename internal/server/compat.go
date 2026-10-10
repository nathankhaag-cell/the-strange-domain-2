package server

// What this node can do, reported by GET /api/v1/info so clients built at a
// different time (the phone app bundles its own copy) can adapt.
//
//   - APILevel goes up when the node gains endpoints a client may rely on.
//     A node that does not report it is level 1 (v1.0.0).
//   - Features lists optional capabilities by name. Clients turn off the
//     matching controls when a name is missing.
//   - MinClientAPI is the oldest client API level the node still works with.
//     The node never refuses older clients itself; clients use this only to
//     tell the person to update.
const (
	APILevel     = 4
	MinClientAPI = 1
)

// Features are the optional capabilities every node of this version has.
// "calls" (level 3: /api/v1/calls and /api/v1/rtc) and "video" are added
// when they are turned on (see Server.features). "bans" (level 4) is
// temporary bans (duration_seconds) and GET /api/v1/domains/{id}/bans.
var Features = []string{"attachments", "avatars", "bans"}
