package scenarios

import (
 "fmt"
 "strings"

 "github.com/hazyforge/anvil-agents/internal/onechat"
)

type ExpectationResult struct {
 Rule string `json:"rule"`
 Pass bool `json:"pass"`
 Reason string `json:"reason"`
}

func contains(values []string, value string) bool {
 for _, v := range values { if v == value { return true } }
 return false
}

func matches(m Matcher, a onechat.Action, labels map[string]string) bool {
 if m.Action != "" && m.Action != a.Type { return false }
 if m.Session != "" && (labels[m.Session] == "" || labels[m.Session] != a.SessionID) { return false }
 if m.Setup != "" && m.Setup != a.SetupID { return false }
 text := a.Text
 if a.Type == "send" || a.Type == "create" { text = a.Message }
 for _, word := range m.Mentions { if !strings.Contains(strings.ToLower(text), strings.ToLower(word)) { return false } }
 if len(m.AnyOf) > 0 {
  for _, alt := range m.AnyOf { if matches(alt, a, labels) { return true } }
  return false
 }
 return true
}

func evaluate(expect *Expectation, decision onechat.Decision, labels map[string]string) []ExpectationResult {
 results := []ExpectationResult{}
 if expect == nil { return results }
 add := func(rule string, pass bool, reason string) { results = append(results, ExpectationResult{Rule: rule, Pass: pass, Reason: reason}) }
 if expect.Actions != nil {
  pass, reason := true, "every action has an allowed type"
  for _, a := range decision.Actions { if !contains(expect.Actions, a.Type) { pass, reason = false, "unexpected action "+a.Type; break } }
  add("actions", pass, reason)
 }
 for i, matcher := range expect.Does {
  pass := false
  for _, a := range decision.Actions { if matches(matcher, a, labels) { pass = true; break } }
  reason := "matched an action"
  if !pass { reason = fmt.Sprintf("no action matched %+v", matcher) }
  add(fmt.Sprintf("does[%d]", i), pass, reason)
 }
 for _, forbidden := range expect.Not {
  pass := true
  for _, a := range decision.Actions { if a.Type == forbidden { pass = false } }
  reason := "no "+forbidden+" action"
  if !pass { reason = "found forbidden action "+forbidden }
  add("not:"+forbidden, pass, reason)
 }
 for _, label := range expect.NotSessions {
  pass := true
  for _, a := range decision.Actions { if labels[label] != "" && a.SessionID == labels[label] { pass = false } }
  reason := "no action targets "+label
  if !pass { reason = "an action targets forbidden session "+label }
  add("notSessions:"+label, pass, reason)
 }
 for _, setup := range expect.NotSetups {
  pass := true
  for _, a := range decision.Actions { if a.Type == "create" && a.SetupID == setup { pass = false } }
  reason := "no create uses "+setup
  if !pass { reason = "create uses forbidden setup "+setup }
  add("notSetups:"+setup, pass, reason)
 }
 if expect.Intent != "" {
  pass := decision.Intent == "" || decision.Intent == expect.Intent
  reason := "intent is "+decision.Intent
  if decision.Intent == "" { reason = "decision has no intent; not checked" } else if !pass { reason += "; expected "+expect.Intent }
  add("intent", pass, reason)
 }
 return results
}
