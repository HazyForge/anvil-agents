package scenarios

import (
 "context"
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "sort"
 "strings"
 "time"

 "github.com/google/uuid"
 "github.com/hazyforge/anvil-agents/internal/onechat"
)

// Each repetition owns its home and state. No harness or cloud credentials are
// used to construct context, and no process-wide HOME change can race a case.
type seededWorld struct {
 suite string
 home string
 config onechat.Config
 sessions map[string]onechat.Session
 history []onechat.Message
 cloudError string
}

func newSeededWorld(suite Suite, config onechat.Config, dir string) (*seededWorld, error) {
 home := filepath.Join(dir, "home")
 root := filepath.Join(home, "code")
 if err := os.MkdirAll(root, 0700); err != nil { return nil, err }
 config.StateDir = filepath.Join(dir, "state")
 config.Cloud.SessionFile = filepath.Join(dir, "unused-session.json")
 config.Cloud.TokenCommand = nil
 config.Local.WorkdirRoots = []string{root}
 if err := config.Validate(); err != nil { return nil, err }
 return &seededWorld{suite: suite.Name, home: home, config: config, sessions: map[string]onechat.Session{}, history: []onechat.Message{}}, nil
}

func (w *seededWorld) workdir(path string) (string, error) {
 var mapped string
 switch {
 case path == "~": mapped = w.home
 case strings.HasPrefix(path, "~/"): mapped = filepath.Join(w.home, filepath.FromSlash(path[2:]))
 case filepath.IsAbs(path):
  // Captures retain real absolute paths. Recreate them beneath the synthetic
  // root so replay never creates a directory in the person's checkout.
  mapped = filepath.Join(w.home, "code", "captured", strings.TrimLeft(strings.TrimPrefix(filepath.Clean(path), filepath.VolumeName(path)), `/\`))
 default:
  if strings.HasPrefix(path, "~") { return "", fmt.Errorf("workdir %q must use ~ or ~/", path) }
  mapped = filepath.Join(w.home, "code", path)
 }
 rel, err := filepath.Rel(w.home, mapped)
 if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) { return "", fmt.Errorf("seeded workdir %q escapes the temporary home", path) }
 if err := os.MkdirAll(mapped, 0700); err != nil { return "", err }
 return mapped, nil
}

func seededMessages(seed []MessageSeed, at time.Time) ([]onechat.Message, error) {
 out := make([]onechat.Message, 0, len(seed))
 for _, m := range seed {
  when := at
  if m.At != nil { when = *m.At } else if m.Ago != "" {
   ago, err := ParseAgo(m.Ago); if err != nil { return nil, err }; when = at.Add(-ago)
  }
  out = append(out, onechat.Message{Role: m.Role, Text: m.Text, At: when})
 }
 return out, nil
}

func mergeMessages(old []onechat.Message, replace, appendSeed []MessageSeed, at time.Time) ([]onechat.Message, error) {
 out := append([]onechat.Message{}, old...)
 if replace != nil {
  var err error
  out, err = seededMessages(replace, at); if err != nil { return nil, err }
 }
 more, err := seededMessages(appendSeed, at); if err != nil { return nil, err }
 out = append(out, more...)
 sort.SliceStable(out, func(i,j int) bool { return out[i].At.Before(out[j].At) })
 return out, nil
}

func (w *seededWorld) apply(seed Seed, at time.Time) error {
 for _, patch := range seed.Sessions {
  s, exists := w.sessions[patch.Label]
  if !exists {
   s = onechat.Session{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("onechat/scenario/"+w.suite+"/"+patch.Label)).String(), Title: patch.Label, State: "idle", CreatedAt: at, UpdatedAt: at, Transcript: []onechat.Message{}}
  }
  for _, field := range []struct{dst *string; src *string}{
   {&s.Location, patch.Location}, {&s.SetupID, patch.Setup}, {&s.Title, patch.Title}, {&s.Summary, patch.Summary}, {&s.State, patch.State},
  } { if field.src != nil { *field.dst = *field.src } }
  for _, setup := range w.config.Setups {
   if setup.ID != s.SetupID { continue }
   if s.Location == "" { s.Location = setup.Location }
   s.Harness, s.Sandbox = setup.Harness, setup.Sandbox
   s.Namespace, s.Profile = setup.Namespace, setup.Profile
  }
  if patch.Harness != nil { s.Harness = *patch.Harness }
  if s.Location == "cloud" { s.BaseURL, s.ThreadID = w.config.Cloud.BaseURL, s.ID }
  if patch.Workdir != nil {
   s.Workdir = *patch.Workdir
   if s.Location == "local" && s.Workdir != "" {
    var err error
    s.Workdir, err = w.workdir(s.Workdir); if err != nil { return err }
   }
  }
  if patch.CreatedAt != nil { s.CreatedAt = *patch.CreatedAt }
  if patch.UpdatedAt != nil { s.UpdatedAt = *patch.UpdatedAt }
  if patch.UpdatedAgo != nil {
   ago, err := ParseAgo(*patch.UpdatedAgo); if err != nil { return err }; s.UpdatedAt = at.Add(-ago)
  }
  var err error
  s.Transcript, err = mergeMessages(s.Transcript, patch.Transcript, patch.TranscriptAppend, at)
  if err != nil { return err }
  w.sessions[patch.Label] = s
 }
 var err error
 w.history, err = mergeMessages(w.history, seed.History, seed.HistoryAppend, at)
 if seed.Cloud != nil { w.cloudError = seed.Cloud.Error }
 return err
}

func (w *seededWorld) after(step Step, at time.Time) error {
 if step.After == nil { return nil }
 w.history = append(w.history, onechat.Message{Role: "user", Text: step.Say, At: at}, onechat.Message{Role: "assistant", Text: step.After.Answer, At: at})
 return w.apply(Seed{Sessions: step.After.Sessions}, at)
}

func (w *seededWorld) manager(decider onechat.Decider) (*onechat.Manager, error) {
 state := onechat.State{Sessions: []onechat.Session{}, History: w.history, CloudBindings: map[string]string{}}
 cloud := &seedBackend{failure: w.cloudError}
 for _, s := range w.sessions {
  if s.Location == "cloud" { cloud.sessions = append(cloud.sessions, s); state.CloudBindings[s.ID] = s.SetupID } else { state.Sessions = append(state.Sessions, s) }
 }
 sort.Slice(state.Sessions, func(i,j int) bool { return state.Sessions[i].ID < state.Sessions[j].ID })
 raw, err := json.Marshal(state); if err != nil { return nil, err }
 if err := os.MkdirAll(w.config.StateDir, 0700); err != nil { return nil, err }
 if err := writePrivateFile(filepath.Join(w.config.StateDir, "onechat.json"), raw); err != nil { return nil, err }
 return onechat.NewManager(w.config, decider, &seedBackend{}, cloud)
}

func (w *seededWorld) labels() map[string]string {
 labels := map[string]string{}
 for label, s := range w.sessions { labels[label] = s.ID }
 return labels
}

// Execution is deliberately unavailable even if a future runner accidentally
// calls Turn instead of DecideTurn.
type seedBackend struct { sessions []onechat.Session; failure string }
func (b *seedBackend) List(context.Context) ([]onechat.Session, error) {
 if b.failure != "" { return nil, fmt.Errorf("%s", b.failure) }
 return b.sessions, nil
}
func (*seedBackend) Create(context.Context, onechat.Setup, string, string) (onechat.Session, onechat.Receipt, error) {
 return onechat.Session{}, onechat.Receipt{}, fmt.Errorf("decide scenarios must never execute create")
}
func (*seedBackend) Send(context.Context, onechat.Setup, onechat.Session, string) (onechat.ActionResult, error) {
 return onechat.ActionResult{}, fmt.Errorf("decide scenarios must never execute send")
}
