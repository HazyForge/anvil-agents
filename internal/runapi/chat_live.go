package runapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agentsv1alpha1 "github.com/hazyforge/anvil-agents/api/v1alpha1"
	"github.com/hazyforge/anvil-agents/internal/chat"
)

const (
	liveReplyEngine          = "entity-wrapper"
	liveReplyMaxSpawn        = 3
	defaultLiveReplyTimeout  = 45 * time.Second
	liveReplyProfilePrefix   = "entity-"
	liveReplyHarnessProfile  = "demo-runtime"
)

type chatReplyPlanner interface {
	Plan(ctx context.Context, input chatReplyInput) (chatReplyPlan, error)
}

type chatReplyInput struct {
	Namespace   string
	ThreadID    string
	ProfileName string
	UserContent string
}

type chatReplyPlan struct {
	CoordinatorSay string
	Spawn          []chatSpawnSpec
}

type chatSpawnSpec struct {
	Name        string
	Role        string
	Say         string
	Description string
}

func (server *Server) liveEntityReplyEnabled() bool {
	return server != nil &&
		server.config.Chat.Enabled &&
		server.config.Composition.WriteEnabled &&
		server.writes != nil
}

func (server *Server) buildLiveReplies(ctx context.Context, namespace, threadID, profileName, userContent string) []chat.Message {
	planner := server.chatPlanner
	if planner == nil {
		planner = grokThenHeuristicPlanner{}
	}
	plan, err := planner.Plan(ctx, chatReplyInput{
		Namespace:   namespace,
		ThreadID:    threadID,
		ProfileName: profileName,
		UserContent: userContent,
	})
	if err != nil || strings.TrimSpace(plan.CoordinatorSay) == "" {
		plan = heuristicChatPlan(userContent, profileName)
	}
	plan = normalizeChatPlan(plan, profileName, userContent)

	var replies []chat.Message
	coordinator := strings.TrimSpace(profileName)
	if coordinator == "" {
		coordinator = "coordinator"
	}
	replies = append(replies, chat.Message{
		Role:     chat.RoleAssistant,
		Content:  plan.CoordinatorSay,
		Metadata: liveReplyMetadata(coordinator, nil),
	})
	for _, spawn := range plan.Spawn {
		created, createErr := server.spawnAgentRunProfile(ctx, namespace, spawn)
		metaNames := []string{spawn.Name}
		if createErr != nil {
			server.log.Error(createErr, "live chat spawn profile", "namespace", namespace, "name", spawn.Name)
			replies = append(replies, chat.Message{
				Role:     chat.RoleAssistant,
				Content:  fmt.Sprintf("I could not create AgentRunProfile %q: %v", spawn.Name, createErr),
				Metadata: liveReplyMetadata(coordinator, nil),
			})
			continue
		}
		if created != spawn.Name {
			metaNames = []string{created}
			spawn.Name = created
		}
		replies = append(replies, chat.Message{
			Role:     chat.RoleAssistant,
			Content:  spawn.Say,
			Metadata: liveReplyMetadata(spawn.Name, metaNames),
		})
	}
	return replies
}

func liveReplyMetadata(agent string, spawned []string) json.RawMessage {
	payload := map[string]any{
		"stub":    false,
		"engine":  liveReplyEngine,
		"agent":   agent,
		"spawned": spawned,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{"stub":false,"engine":"entity-wrapper"}`)
	}
	return raw
}

func (server *Server) spawnAgentRunProfile(ctx context.Context, namespace string, spawn chatSpawnSpec) (string, error) {
	name := dnsLabel(liveReplyProfilePrefix + spawn.Name)
	if name == "" || name == liveReplyProfilePrefix {
		name = dnsLabel(fmt.Sprintf("%s%v", liveReplyProfilePrefix, time.Now().Unix()%100000))
	}
	description := strings.TrimSpace(spawn.Description)
	if description == "" {
		description = fmt.Sprintf("Kind entity-chat spawn (%s).", strings.TrimSpace(spawn.Role))
	}
	role := strings.TrimSpace(spawn.Role)
	if role == "" {
		role = spawn.Name
	}
	profile := &agentsv1alpha1.AgentRunProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: agentsv1alpha1.AgentRunProfileSpec{
			Description: description,
			Scope: agentsv1alpha1.AgentRunScopeSpec{
				Summary: "Spawned from entity standing chat on Kind. No new AgentRun.",
			},
			HarnessProfileRef: &agentsv1alpha1.NamespacedObjectReference{Name: liveReplyHarnessProfile},
			Harness: agentsv1alpha1.AgentRunHarnessSpec{
				Intent:       agentsv1alpha1.AgentRunIntentObserve,
				SystemPrompt: fmt.Sprintf("You are %s. Speak only as this role. You do not receive peer credentials.", role),
			},
		},
	}
	stampConsoleManaged(profile)
	if err := server.writes.Create(ctx, profile); err != nil {
		return "", err
	}
	return name, nil
}

type grokThenHeuristicPlanner struct{}

func (grokThenHeuristicPlanner) Plan(ctx context.Context, input chatReplyInput) (chatReplyPlan, error) {
	if plan, err := planWithGrok(ctx, input); err == nil && strings.TrimSpace(plan.CoordinatorSay) != "" {
		return normalizeChatPlan(plan, input.ProfileName, input.UserContent), nil
	}
	return heuristicChatPlan(input.UserContent, input.ProfileName), nil
}

func planWithGrok(ctx context.Context, input chatReplyInput) (chatReplyPlan, error) {
	command := strings.TrimSpace(os.Getenv("ANVIL_AGENTS_CHAT_LIVE_REPLY_COMMAND"))
	if command == "" {
		if _, err := exec.LookPath("grok"); err != nil {
			return chatReplyPlan{}, fmt.Errorf("grok CLI is not available")
		}
		command = "grok"
	}
	timeout := defaultLiveReplyTimeout
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	schema := `{"type":"object","additionalProperties":false,"required":["say","spawn"],"properties":{"say":{"type":"string"},"spawn":{"type":"array","maxItems":3,"items":{"type":"object","additionalProperties":false,"required":["name","role","say"],"properties":{"name":{"type":"string"},"role":{"type":"string"},"say":{"type":"string"},"description":{"type":"string"}}}}}}`
	system := "You are the entity wrapper for Anvil Agents standing chat. Reply as the current AgentRunProfile. When the human asks to create, hire, or spawn agents, put 1-3 DNS-label names in spawn. Each spawned agent must speak as itself in say. Never echo the user verbatim. Never request secrets."
	user := fmt.Sprintf("namespace=%s profile=%s\n\n%s", input.Namespace, input.ProfileName, input.UserContent)
	cmd := exec.CommandContext(cmdCtx, command, "-p", user, "--json-schema", schema, "--system-prompt-override", system, "-m", "grok-4.5")
	cmd.Dir = os.TempDir()
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return chatReplyPlan{}, fmt.Errorf("live reply command: %w", err)
	}
	return decodeChatPlanJSON(stdout.Bytes())
}

func decodeChatPlanJSON(raw []byte) (chatReplyPlan, error) {
	trimmed := bytes.TrimSpace(raw)
	if idx := bytes.IndexByte(trimmed, '{'); idx >= 0 {
		if end := bytes.LastIndexByte(trimmed, '}'); end > idx {
			trimmed = trimmed[idx : end+1]
		}
	}
	var parsed struct {
		Say   string `json:"say"`
		Spawn []struct {
			Name        string `json:"name"`
			Role        string `json:"role"`
			Say         string `json:"say"`
			Description string `json:"description"`
		} `json:"spawn"`
	}
	if err := json.Unmarshal(trimmed, &parsed); err != nil {
		return chatReplyPlan{}, err
	}
	plan := chatReplyPlan{CoordinatorSay: parsed.Say}
	for _, item := range parsed.Spawn {
		plan.Spawn = append(plan.Spawn, chatSpawnSpec{
			Name:        item.Name,
			Role:        item.Role,
			Say:         item.Say,
			Description: item.Description,
		})
	}
	return plan, nil
}

var (
	spawnIntent = regexp.MustCompile(`(?i)\b(hire|spawn|recruit|create|add|stand up|bring on)\b.*\b(agents?|profiles?|researcher|reviewer|worker|peer|teammate|entities)\b|\b(another|more)\s+agents?`)
	namedAgent = regexp.MustCompile(`(?i)\b(?:named|called)\s+([a-z0-9][a-z0-9,\s-]{0,80})`)
	aRoleAgent = regexp.MustCompile(`(?i)\ba[n]?\s+([a-z][a-z0-9-]{0,31})(?:\s+and\s+a[n]?\s+([a-z][a-z0-9-]{0,31}))?`)
)

func heuristicChatPlan(userContent, profileName string) chatReplyPlan {
	coordinator := strings.TrimSpace(profileName)
	if coordinator == "" {
		coordinator = "coordinator"
	}
	plan := chatReplyPlan{
		CoordinatorSay: fmt.Sprintf("%s here. I will keep this on the standing-chat store and only create console-managed AgentRunProfiles — no AgentRuns.", coordinator),
	}
	if !wantsSpawn(userContent) {
		plan.CoordinatorSay = fmt.Sprintf("%s here. I read your note and I am staying in this thread. Ask me to hire another agent if you want a new AgentRunProfile.", coordinator)
		return plan
	}
	names := extractSpawnNames(userContent)
	if len(names) == 0 {
		names = []string{"researcher", "reviewer"}
	}
	if len(names) > liveReplyMaxSpawn {
		names = names[:liveReplyMaxSpawn]
	}
	plan.CoordinatorSay = fmt.Sprintf("%s here. I am creating AgentRunProfiles for %s and asking them to speak in this thread.", coordinator, strings.Join(names, " and "))
	for _, name := range names {
		plan.Spawn = append(plan.Spawn, chatSpawnSpec{
			Name:        name,
			Role:        name,
			Say:         fmt.Sprintf("%s checking in. I am a new AgentRunProfile spawned from this entity chat. I can see this thread; I do not share credentials with peers.", name),
			Description: fmt.Sprintf("Entity-chat spawned %s role.", name),
		})
	}
	return plan
}

func wantsSpawn(userContent string) bool {
	return spawnIntent.MatchString(userContent)
}

func extractSpawnNames(userContent string) []string {
	seen := map[string]bool{}
	var names []string
	add := func(raw string) {
		label := dnsLabel(raw)
		if label == "" || seen[label] || label == "agent" || label == "profile" || label == "entity" {
			return
		}
		seen[label] = true
		names = append(names, label)
	}
	if match := namedAgent.FindStringSubmatch(userContent); len(match) > 1 {
		for _, name := range regexp.MustCompile(`(?i)\s*(?:,|/|and)\s*`).Split(match[1], -1) {
			add(name)
		}
	}
	if role := aRoleAgent.FindStringSubmatch(userContent); len(role) > 1 {
		add(role[1])
		if len(role) > 2 {
			add(role[2])
		}
	}
	return names
}

func normalizeChatPlan(plan chatReplyPlan, profileName, userContent string) chatReplyPlan {
	if strings.TrimSpace(plan.CoordinatorSay) == "" {
		fallback := heuristicChatPlan(userContent, profileName)
		plan.CoordinatorSay = fallback.CoordinatorSay
		if len(plan.Spawn) == 0 {
			plan.Spawn = fallback.Spawn
		}
	}
	if len(plan.Spawn) > liveReplyMaxSpawn {
		plan.Spawn = plan.Spawn[:liveReplyMaxSpawn]
	}
	for i := range plan.Spawn {
		plan.Spawn[i].Name = dnsLabel(plan.Spawn[i].Name)
		if plan.Spawn[i].Name == "" {
			plan.Spawn[i].Name = fmt.Sprintf("peer-%d", i+1)
		}
		if strings.TrimSpace(plan.Spawn[i].Role) == "" {
			plan.Spawn[i].Role = plan.Spawn[i].Name
		}
		if strings.TrimSpace(plan.Spawn[i].Say) == "" {
			plan.Spawn[i].Say = fmt.Sprintf("%s reporting. Spawned as an AgentRunProfile from entity chat.", plan.Spawn[i].Name)
		}
	}
	return plan
}

func dnsLabel(raw string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		if r == '-' || r == '_' || r == ' ' {
			b.WriteByte('-')
		}
	}
	label := strings.Trim(b.String(), "-")
	for strings.Contains(label, "--") {
		label = strings.ReplaceAll(label, "--", "-")
	}
	if len(label) > 40 {
		label = strings.Trim(label[:40], "-")
	}
	return label
}
