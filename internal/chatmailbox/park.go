package chatmailbox

import "strings"

// Action is what the mailbox API may do with a human or member line.
// store is parking: persist, do not spawn an AgentRun, do not PATCH a Job.
type Action string

const (
	ActionStore  Action = "store"
	ActionReject Action = "reject"
)

// Delivery is enough to decide park vs reject. AdapterCanInterrupt must be
// false until a runner image can stop a generation. Chat must not offer Stop
// while that is false.
type Delivery struct {
	Purpose             string
	Kind                Kind
	HasActiveSession    bool
	AdapterCanInterrupt bool
}

// Decision is the honest outcome of an inbound chat line.
type Decision struct {
	Action              Action
	SpawnAgentRun       bool
	InterruptGeneration bool
	Reason              string
}

// Park is Substrate request-parking applied to chat: inbound work waits in
// the store. Transient "no session" is not 503 and is not a new AgentRun.
// Fail-fast when the purpose cannot receive mailbox rows.
func Park(in Delivery) Decision {
	if !MayReceiveMailbox(in.Purpose) {
		return Decision{
			Action: ActionReject,
			Reason: "fire-and-forget AgentRuns never receive chat mailbox rows",
		}
	}
	kind := in.Kind
	if kind == "" {
		kind = KindUtterance
	}
	switch kind {
	case KindUtterance:
		reason := "queued until the interactive session is idle"
		if !in.HasActiveSession {
			reason = "stored until an interactive session is summoned"
		}
		return Decision{Action: ActionStore, Reason: reason}
	case KindInterrupt:
		if !in.AdapterCanInterrupt {
			return Decision{
				Action: ActionStore,
				Reason: "interrupt stored; no harness adapter can stop a generation",
			}
		}
		if !in.HasActiveSession {
			return Decision{
				Action: ActionStore,
				Reason: "interrupt stored until an interactive session is summoned",
			}
		}
		return Decision{
			Action:              ActionStore,
			InterruptGeneration: true,
			Reason:              "stop the current generation then ingest",
		}
	default:
		return Decision{Action: ActionReject, Reason: "unsupported mailbox kind"}
	}
}

// SameFlight reports that two deliveries should share one in-flight session
// instead of launching a sibling AgentRun.
func SameFlight(a, b string) bool {
	return strings.TrimSpace(a) != "" && strings.TrimSpace(a) == strings.TrimSpace(b)
}
