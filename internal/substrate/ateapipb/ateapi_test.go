package ateapipb

import (
	"encoding/hex"
	"testing"

	"github.com/golang/protobuf/proto"
)

// TestWireGoldenVectors pins the hand-written stubs to exact protobuf bytes
// for ATE Helm 0.0.8 (kagent-dev/substrate v0.0.8). If upstream renumbers a
// lifecycle field, this test fails and the refresh procedure in ateapi.go
// applies. Vectors:
//
//	ref:       ObjectRef{atespace: "agents", name: "chat-1"}
//	selector:  Selector{match_labels: {"anvil.hazyforge.io/worker-pool": "warm"}}
//	meta:      ResourceMetadata{atespace: "agents", name: "chat-1", uid: "uid-1", version: 3}
//	actor:     ATE 0.0.8 Actor (actor_id/atespace/template namespace+name/SUSPENDED/pool)
//	getReq:    GetActorRequest{actor: ref}
//	createReq: CreateActorRequest{actor_ref, actor_template_namespace, actor_template_name}
//	resumeResp: ResumeActorResponse{actor: {atespace a, actor_id b}, resumed: true}
func TestWireGoldenVectors(t *testing.T) {
	t.Parallel()

	ref := &ObjectRef{Atespace: "agents", Name: "chat-1"}
	vectors := map[string]struct {
		message proto.Message
		wantHex string
	}{
		"ref": {
			message: ref,
			wantHex: "0a066167656e74731206636861742d31",
		},
		"selector": {
			message: &Selector{MatchLabels: map[string]string{"anvil.hazyforge.io/worker-pool": "warm"}},
			wantHex: "0a260a1e616e76696c2e68617a79666f7267652e696f2f776f726b65722d706f6f6c12047761726d",
		},
		"meta": {
			message: &ResourceMetadata{Atespace: "agents", Name: "chat-1", Uid: "uid-1", Version: 3},
			wantHex: "0a066167656e74731206636861742d311a057569642d312003",
		},
		"actor": {
			message: &Actor{
				ActorId:                "chat-1",
				Version:                3,
				ActorTemplateNamespace: "agents",
				ActorTemplateName:      "standing-chat",
				Status:                 ActorState_ACTOR_STATE_SUSPENDED,
				WorkerSelector:         &Selector{MatchLabels: map[string]string{"anvil.hazyforge.io/worker-pool": "warm"}},
				Atespace:               "agents",
			},
			wantHex: "0a06636861742d3110031a066167656e7473220d7374616e64696e672d6368617428046a280a260a1e616e76696c2e68617a79666f7267652e696f2f776f726b65722d706f6f6c12047761726d7a066167656e7473",
		},
		"getReq": {
			message: &GetActorRequest{Actor: ref},
			wantHex: "0a100a066167656e74731206636861742d31",
		},
		"createReq": {
			message: &CreateActorRequest{
				ActorRef:               ref,
				ActorTemplateNamespace: "agents",
				ActorTemplateName:      "standing-chat",
			},
			wantHex: "0a100a066167656e74731206636861742d3112066167656e74731a0d7374616e64696e672d63686174",
		},
		"resumeResp": {
			message: &ResumeActorResponse{Actor: &Actor{Atespace: "a", ActorId: "b"}, Resumed: true},
			wantHex: "0a060a01627a01611001",
		},
	}
	for name, vector := range vectors {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			encoded, err := proto.Marshal(vector.message)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if got := hex.EncodeToString(encoded); got != vector.wantHex {
				t.Fatalf("wire bytes = %s, want %s", got, vector.wantHex)
			}
			decoded := proto.Clone(vector.message)
			decoded.Reset()
			if err := proto.Unmarshal(encoded, decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			reencoded, err := proto.Marshal(decoded)
			if err != nil {
				t.Fatalf("re-marshal: %v", err)
			}
			if got := hex.EncodeToString(reencoded); got != vector.wantHex {
				t.Fatalf("round-trip bytes = %s, want %s", got, vector.wantHex)
			}
		})
	}
}

// TestRevertingWireValue pins ACTOR_STATE_REVERTING = 9 as a status varint
// (bytes 0809). ATE 0.0.8 never sends it; later ateapi does.
func TestRevertingWireValue(t *testing.T) {
	t.Parallel()

	if ActorState_ACTOR_STATE_REVERTING != ActorState(9) {
		t.Fatalf("REVERTING = %d, want 9", int32(ActorState_ACTOR_STATE_REVERTING))
	}
	if got := ActorState_ACTOR_STATE_REVERTING.String(); got != "ACTOR_STATE_REVERTING" {
		t.Fatalf("REVERTING String() = %q", got)
	}
	status := &ActorStatus{State: ActorState_ACTOR_STATE_REVERTING}
	encoded, err := proto.Marshal(status)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := hex.EncodeToString(encoded); got != "0809" {
		t.Fatalf("reverting status bytes = %s, want 0809", got)
	}
	decoded := &ActorStatus{}
	if err := proto.Unmarshal(encoded, decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.GetState() != ActorState_ACTOR_STATE_REVERTING {
		t.Fatalf("round-trip state = %v, want REVERTING", decoded.GetState())
	}
}

// TestServicePaths pins the gRPC method paths the server routes on.
func TestServicePaths(t *testing.T) {
	t.Parallel()

	if Control_ServiceDesc.ServiceName != "ateapi.Control" {
		t.Fatalf("service = %q, want ateapi.Control", Control_ServiceDesc.ServiceName)
	}
	want := map[string]string{
		"GetActor":     Control_GetActor_FullMethodName,
		"CreateActor":  Control_CreateActor_FullMethodName,
		"SuspendActor": Control_SuspendActor_FullMethodName,
		"PauseActor":   Control_PauseActor_FullMethodName,
		"ResumeActor":  Control_ResumeActor_FullMethodName,
	}
	for method, path := range want {
		if path != "/ateapi.Control/"+method {
			t.Fatalf("method %s path = %q", method, path)
		}
	}
	if len(Control_ServiceDesc.Methods) != len(want) {
		t.Fatalf("service methods = %d, want %d", len(Control_ServiceDesc.Methods), len(want))
	}
}
