package ateapipb

import (
	"encoding/hex"
	"testing"

	"github.com/golang/protobuf/proto"
)

// TestWireGoldenVectors pins the hand-written stubs to exact protobuf bytes
// captured from the upstream-generated stubs (protoc-gen-go from ateapi.proto
// at af2477e). If upstream renumbers a lifecycle field, this test fails and
// the refresh procedure in ateapi.go applies. Vectors:
//
//	ref:      ObjectRef{atespace: "agents", name: "chat-1"}
//	selector: Selector{match_labels: {"anvil.hazyforge.io/worker-pool": "warm"}}
//	meta:     ResourceMetadata{atespace: "agents", name: "chat-1", uid: "uid-1", version: 3}
//	actor:    full Actor with metadata/template/selector/SUSPENDED status
//	getReq:   GetActorRequest{actor: ref}
//	createReq: CreateActorRequest{actor: {metadata: ref}}
//	resumeResp: ResumeActorResponse{actor: {metadata: {a/b}}, resumed: true}
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
				Metadata:       &ResourceMetadata{Atespace: "agents", Name: "chat-1", Uid: "uid-1"},
				ActorTemplate:  &ObjectRef{Atespace: "agents", Name: "standing-chat"},
				WorkerSelector: &Selector{MatchLabels: map[string]string{"anvil.hazyforge.io/worker-pool": "warm"}},
				Status:         &ActorStatus{State: ActorState_ACTOR_STATE_SUSPENDED},
			},
			wantHex: "0a170a066167656e74731206636861742d311a057569642d3122170a066167656e7473120d7374616e64696e672d636861742a280a260a1e616e76696c2e68617a79666f7267652e696f2f776f726b65722d706f6f6c12047761726d3a020804",
		},
		"getReq": {
			message: &GetActorRequest{Actor: ref},
			wantHex: "0a100a066167656e74731206636861742d31",
		},
		"createReq": {
			message: &CreateActorRequest{Actor: &Actor{Metadata: &ResourceMetadata{Atespace: "agents", Name: "chat-1"}}},
			wantHex: "0a120a100a066167656e74731206636861742d31",
		},
		"resumeResp": {
			message: &ResumeActorResponse{Actor: &Actor{Metadata: &ResourceMetadata{Atespace: "a", Name: "b"}}, Resumed: true},
			wantHex: "0a080a060a01611201621001",
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
			// The bytes must also round-trip through unmarshal.
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
