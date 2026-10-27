package scenarios

var States = []string{
	"fresh", "idle", "overlapping", "session-running", "mid-turn", "asked",
	"failed", "cancelled", "long-history", "cloud-down", "stale-running",
}

var Moves = []string{
	"kickoff", "continue", "ship", "status", "impatience", "recall", "explain",
	"complete-last", "self-correct", "scope-add", "gate", "pivot", "stop",
	"push-back", "question-agent", "report-action", "set-rule", "delegate",
	"authority", "money", "short-answer", "paste", "noise", "chatter",
	"life-admin", "multi-ask",
}

var Intents = []string{
	"question", "amend_running", "new_work", "status", "decide", "unclear",
	"stop", "chatter", "preference",
}

var Voices = []string{
	"lowercase", "space-question", "typo", "dictation", "misheard", "terse", "frustrated",
}

var ActionTypes = []string{"answer", "read_status", "send", "create", "ask_user", "stop"}
