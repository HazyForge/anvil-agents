// Package scenarios loads and checks synthetic onechat decision scenarios.
package scenarios

import (
	"time"

	"github.com/hazyforge/anvil-agents/internal/onechat"
)

type Suite struct {
	Name  string `json:"suite"`
	State string `json:"state"`
	World World  `json:"world"`
	Seed  Seed   `json:"seed,omitempty"`
	Cases []Case `json:"cases"`
}

type World struct {
	Name       string              `json:"world,omitempty"`
	Config     string              `json:"config,omitempty"`
	Setups     []onechat.Setup     `json:"setups,omitempty"`
	Backend    onechat.CloudConfig `json:"backend,omitempty"`
	ConfigData onechat.Config      `json:"-"`
	Seed
}

type Seed struct {
	Sessions      []SessionSeed `json:"sessions,omitempty"`
	History       []MessageSeed `json:"history,omitempty"`
	HistoryAppend []MessageSeed `json:"history+,omitempty"`
	Cloud         *CloudSeed    `json:"cloud,omitempty"`
}

type CloudSeed struct {
	Error string `json:"error"`
}

// Pointers distinguish a field left alone from an explicit empty patch.
type SessionSeed struct {
	Label            string        `json:"label"`
	Location         *string       `json:"location,omitempty"`
	Setup            *string       `json:"setup,omitempty"`
	Harness          *string       `json:"harness,omitempty"`
	Title            *string       `json:"title,omitempty"`
	Summary          *string       `json:"summary,omitempty"`
	State            *string       `json:"state,omitempty"`
	Workdir          *string       `json:"workdir,omitempty"`
	UpdatedAgo       *string       `json:"updatedAgo,omitempty"`
	CreatedAt        *time.Time    `json:"createdAt,omitempty"`
	UpdatedAt        *time.Time    `json:"updatedAt,omitempty"`
	Transcript       []MessageSeed `json:"transcript,omitempty"`
	TranscriptAppend []MessageSeed `json:"transcript+,omitempty"`
	NativeSessionID  *string       `json:"nativeSessionId,omitempty"`
	Sandbox          *string       `json:"sandbox,omitempty"`
	BaseURL          *string       `json:"baseURL,omitempty"`
	Namespace        *string       `json:"namespace,omitempty"`
	ThreadID         *string       `json:"threadId,omitempty"`
	Profile          *string       `json:"profile,omitempty"`
}

type MessageSeed struct {
	Role string     `json:"role"`
	Text string     `json:"text"`
	Ago  string     `json:"ago,omitempty"`
	At   *time.Time `json:"at,omitempty"`
}

type Case struct {
	Name   string       `json:"name"`
	State  string       `json:"state,omitempty"`
	Moves  []string     `json:"moves,omitempty"`
	Voice  []string     `json:"voice,omitempty"`
	From   string       `json:"from,omitempty"`
	Seed   Seed         `json:"seed,omitempty"`
	Say    string       `json:"say,omitempty"`
	Expect *Expectation `json:"expect,omitempty"`
	Steps  []Step       `json:"steps,omitempty"`
	Gap    string       `json:"gap,omitempty"`
	Note   string       `json:"note,omitempty"`
}

type Step struct {
	Say    string       `json:"say"`
	Expect *Expectation `json:"expect"`
	After  *After       `json:"after,omitempty"`
	Note   string       `json:"note,omitempty"`
}

type After struct {
	Answer   string        `json:"answer"`
	Sessions []SessionSeed `json:"sessions,omitempty"`
}

type Expectation struct {
	Intent      string    `json:"intent,omitempty"`
	Actions     []string  `json:"actions,omitempty"`
	Does        []Matcher `json:"does,omitempty"`
	Not         []string  `json:"not,omitempty"`
	NotSessions []string  `json:"notSessions,omitempty"`
	NotSetups   []string  `json:"notSetups,omitempty"`
}

type Matcher struct {
	Action   string    `json:"action,omitempty"`
	Session  string    `json:"session,omitempty"`
	Setup    string    `json:"setup,omitempty"`
	Mentions []string  `json:"mentions,omitempty"`
	AnyOf    []Matcher `json:"anyOf,omitempty"`
}

func CaseSteps(c Case) []Step {
	if len(c.Steps) > 0 {
		return c.Steps
	}
	return []Step{{Say: c.Say, Expect: c.Expect, Note: c.Note}}
}
