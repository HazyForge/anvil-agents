// Package ateapipb is a minimal hand-written Go binding for the Substrate
// ATE lifecycle surface (upstream ateapi.Control).
//
// Upstream source: github.com/kagent-dev/substrate ATE Helm 0.0.8
// (pkg/proto/ateapipb/ateapi.proto at tag v0.0.8). Live Primaris ateapi is
// that chart. Later agent-substrate CreateActorRequest nested a full Actor
// (actor_template.atespace); 0.0.8 requires sibling fields
// actor_template_namespace / actor_template_name and wraps Get/Create
// responses. The subset proto next to this file is the source of truth;
// struct tags below copy those field numbers.
//
// Why hand-written: protoc-gen-go output trips gosec G103 (unsafe) and pulls
// a codegen toolchain into the spike; these ~400 lines of plain structs are
// auditable, gosec-clean, and sufficient for the five lifecycle RPCs. Wire
// compatibility is pinned by TestWireGoldenVectors, which asserts exact
// protobuf bytes.
//
// How to refresh when upstream churns:
//  1. Fetch the upstream proto (kagent-dev/substrate v0.0.8 or the live
//     ateapi image's pin) and diff the Control service plus the
//     ObjectRef/Actor/CreateActorRequest messages against ateapi.proto.
//  2. Update the struct tags and method set below to match (field numbers are
//     what matter on the wire; unknown server fields are skipped per proto3
//     semantics).
//  3. Regenerate golden vectors if the lifecycle fields changed and run:
//     go test ./internal/substrate/... ; go build ./...
package ateapipb

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Control method paths. The package/service/method names must match upstream
// exactly: gRPC routes on the path string.
const (
	Control_GetActor_FullMethodName     = "/ateapi.Control/GetActor"
	Control_CreateActor_FullMethodName  = "/ateapi.Control/CreateActor"
	Control_SuspendActor_FullMethodName = "/ateapi.Control/SuspendActor"
	Control_PauseActor_FullMethodName   = "/ateapi.Control/PauseActor"
	Control_ResumeActor_FullMethodName  = "/ateapi.Control/ResumeActor"
)

// ActorState mirrors the upstream ateapi.ActorState enum values 0-9
// (REVERTING = 9 arrived with the RevertActor RPC in 944abe3 and transitions
// the actor back to SUSPENDED; the spike binds no Revert/Delete RPCs).
type ActorState int32

const (
	ActorState_ACTOR_STATE_UNSPECIFIED ActorState = 0
	ActorState_ACTOR_STATE_RESUMING    ActorState = 1
	ActorState_ACTOR_STATE_RUNNING     ActorState = 2
	ActorState_ACTOR_STATE_SUSPENDING  ActorState = 3
	ActorState_ACTOR_STATE_SUSPENDED   ActorState = 4
	ActorState_ACTOR_STATE_PAUSING     ActorState = 5
	ActorState_ACTOR_STATE_PAUSED      ActorState = 6
	ActorState_ACTOR_STATE_CRASHED     ActorState = 7
	ActorState_ACTOR_STATE_DELETING    ActorState = 8
	ActorState_ACTOR_STATE_REVERTING   ActorState = 9
)

var actorStateNames = map[ActorState]string{
	ActorState_ACTOR_STATE_UNSPECIFIED: "ACTOR_STATE_UNSPECIFIED",
	ActorState_ACTOR_STATE_RESUMING:    "ACTOR_STATE_RESUMING",
	ActorState_ACTOR_STATE_RUNNING:     "ACTOR_STATE_RUNNING",
	ActorState_ACTOR_STATE_SUSPENDING:  "ACTOR_STATE_SUSPENDING",
	ActorState_ACTOR_STATE_SUSPENDED:   "ACTOR_STATE_SUSPENDED",
	ActorState_ACTOR_STATE_PAUSING:     "ACTOR_STATE_PAUSING",
	ActorState_ACTOR_STATE_PAUSED:      "ACTOR_STATE_PAUSED",
	ActorState_ACTOR_STATE_CRASHED:     "ACTOR_STATE_CRASHED",
	ActorState_ACTOR_STATE_DELETING:    "ACTOR_STATE_DELETING",
	ActorState_ACTOR_STATE_REVERTING:   "ACTOR_STATE_REVERTING",
}

func (s ActorState) String() string {
	if name, ok := actorStateNames[s]; ok {
		return name
	}
	return fmt.Sprintf("ActorState(%d)", int32(s))
}

// ObjectRef references a Substrate resource by atespace and name
// (upstream ateapi.ObjectRef: atespace = 1, name = 2).
type ObjectRef struct {
	Atespace string `protobuf:"bytes,1,opt,name=atespace,proto3" json:"atespace,omitempty"`
	Name     string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
}

func (m *ObjectRef) Reset()         { *m = ObjectRef{} }
func (m *ObjectRef) String() string { return fmt.Sprintf("atespace:%q name:%q", m.Atespace, m.Name) }
func (*ObjectRef) ProtoMessage()    {}

func (m *ObjectRef) GetAtespace() string {
	if m == nil {
		return ""
	}
	return m.Atespace
}

func (m *ObjectRef) GetName() string {
	if m == nil {
		return ""
	}
	return m.Name
}

// Selector matches worker pools by label (upstream ateapi.Selector:
// match_labels = 1).
type Selector struct {
	MatchLabels map[string]string `protobuf:"bytes,1,rep,name=match_labels,json=matchLabels,proto3" json:"match_labels,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
}

func (m *Selector) Reset()         { *m = Selector{} }
func (m *Selector) String() string { return fmt.Sprintf("match_labels:%v", m.MatchLabels) }
func (*Selector) ProtoMessage()    {}

func (m *Selector) GetMatchLabels() map[string]string {
	if m == nil {
		return nil
	}
	return m.MatchLabels
}

// ResourceMetadata holds the common resource fields the Anvil side reads
// (upstream ateapi.ResourceMetadata: atespace = 1, name = 2, uid = 3,
// version = 4; create_time = 5 and update_time = 6 are server-managed and
// skipped on the wire).
type ResourceMetadata struct {
	Atespace string `protobuf:"bytes,1,opt,name=atespace,proto3" json:"atespace,omitempty"`
	Name     string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	Uid      string `protobuf:"bytes,3,opt,name=uid,proto3" json:"uid,omitempty"`
	Version  int64  `protobuf:"varint,4,opt,name=version,proto3" json:"version,omitempty"`
}

func (m *ResourceMetadata) Reset()         { *m = ResourceMetadata{} }
func (m *ResourceMetadata) String() string { return fmt.Sprintf("atespace:%q name:%q uid:%q", m.Atespace, m.Name, m.Uid) }
func (*ResourceMetadata) ProtoMessage()    {}

func (m *ResourceMetadata) GetAtespace() string {
	if m == nil {
		return ""
	}
	return m.Atespace
}

func (m *ResourceMetadata) GetName() string {
	if m == nil {
		return ""
	}
	return m.Name
}

func (m *ResourceMetadata) GetUid() string {
	if m == nil {
		return ""
	}
	return m.Uid
}

// ActorStatus carries the lifecycle state (upstream ateapi.ActorStatus:
// state = 1; worker assignment, snapshots, and volumes are skipped).
type ActorStatus struct {
	State ActorState `protobuf:"varint,1,opt,name=state,proto3,enum=ateapi.ActorState" json:"state,omitempty"`
}

func (m *ActorStatus) Reset()         { *m = ActorStatus{} }
func (m *ActorStatus) String() string { return fmt.Sprintf("state:%v", m.State) }
func (*ActorStatus) ProtoMessage()    {}

func (m *ActorStatus) GetState() ActorState {
	if m == nil {
		return ActorState_ACTOR_STATE_UNSPECIFIED
	}
	return m.State
}

// Actor is the ATE 0.0.8 lifecycle projection (actor_id = 1, version = 2,
// actor_template_namespace = 3, actor_template_name = 4, status = 5,
// worker_selector = 13, atespace = 15).
type Actor struct {
	ActorId                string     `protobuf:"bytes,1,opt,name=actor_id,json=actorId,proto3" json:"actor_id,omitempty"`
	Version                int64      `protobuf:"varint,2,opt,name=version,proto3" json:"version,omitempty"`
	ActorTemplateNamespace string     `protobuf:"bytes,3,opt,name=actor_template_namespace,json=actorTemplateNamespace,proto3" json:"actor_template_namespace,omitempty"`
	ActorTemplateName      string     `protobuf:"bytes,4,opt,name=actor_template_name,json=actorTemplateName,proto3" json:"actor_template_name,omitempty"`
	Status                 ActorState `protobuf:"varint,5,opt,name=status,proto3,enum=ateapi.ActorState" json:"status,omitempty"`
	WorkerSelector         *Selector  `protobuf:"bytes,13,opt,name=worker_selector,json=workerSelector,proto3" json:"worker_selector,omitempty"`
	Atespace               string     `protobuf:"bytes,15,opt,name=atespace,proto3" json:"atespace,omitempty"`
}

func (m *Actor) Reset() { *m = Actor{} }
func (m *Actor) String() string {
	return fmt.Sprintf("atespace:%q actor_id:%q status:%v", m.GetAtespace(), m.GetActorId(), m.GetStatus())
}
func (*Actor) ProtoMessage() {}

func (m *Actor) GetActorId() string {
	if m == nil {
		return ""
	}
	return m.ActorId
}

func (m *Actor) GetActorTemplateNamespace() string {
	if m == nil {
		return ""
	}
	return m.ActorTemplateNamespace
}

func (m *Actor) GetActorTemplateName() string {
	if m == nil {
		return ""
	}
	return m.ActorTemplateName
}

func (m *Actor) GetWorkerSelector() *Selector {
	if m == nil {
		return nil
	}
	return m.WorkerSelector
}

func (m *Actor) GetStatus() ActorState {
	if m == nil {
		return ActorState_ACTOR_STATE_UNSPECIFIED
	}
	return m.Status
}

func (m *Actor) GetAtespace() string {
	if m == nil {
		return ""
	}
	return m.Atespace
}

type GetActorRequest struct {
	Actor *ObjectRef `protobuf:"bytes,1,opt,name=actor,proto3" json:"actor,omitempty"`
}

func (m *GetActorRequest) Reset()         { *m = GetActorRequest{} }
func (m *GetActorRequest) String() string { return fmt.Sprintf("actor:{%v}", m.GetActor()) }
func (*GetActorRequest) ProtoMessage()    {}

func (m *GetActorRequest) GetActor() *ObjectRef {
	if m == nil {
		return nil
	}
	return m.Actor
}

type GetActorResponse struct {
	Actor *Actor `protobuf:"bytes,1,opt,name=actor,proto3" json:"actor,omitempty"`
}

func (m *GetActorResponse) Reset()         { *m = GetActorResponse{} }
func (m *GetActorResponse) String() string { return fmt.Sprintf("actor:{%v}", m.GetActor()) }
func (*GetActorResponse) ProtoMessage()    {}

func (m *GetActorResponse) GetActor() *Actor {
	if m == nil {
		return nil
	}
	return m.Actor
}

// CreateActorRequest is ATE Helm 0.0.8's create payload: actor_ref plus
// sibling actor_template_namespace / actor_template_name. Later ateapi nested
// those under Actor.actor_template.atespace; 0.0.8 rejects that shape with
// InvalidArgument: actor_template_namespace is required.
type CreateActorRequest struct {
	ActorRef               *ObjectRef `protobuf:"bytes,1,opt,name=actor_ref,json=actorRef,proto3" json:"actor_ref,omitempty"`
	ActorTemplateNamespace string     `protobuf:"bytes,2,opt,name=actor_template_namespace,json=actorTemplateNamespace,proto3" json:"actor_template_namespace,omitempty"`
	ActorTemplateName      string     `protobuf:"bytes,3,opt,name=actor_template_name,json=actorTemplateName,proto3" json:"actor_template_name,omitempty"`
	WorkerSelector         *Selector  `protobuf:"bytes,4,opt,name=worker_selector,json=workerSelector,proto3" json:"worker_selector,omitempty"`
}

func (m *CreateActorRequest) Reset() { *m = CreateActorRequest{} }
func (m *CreateActorRequest) String() string {
	return fmt.Sprintf("actor_ref:{%v} actor_template_namespace:%q actor_template_name:%q", m.GetActorRef(), m.GetActorTemplateNamespace(), m.GetActorTemplateName())
}
func (*CreateActorRequest) ProtoMessage() {}

func (m *CreateActorRequest) GetActorRef() *ObjectRef {
	if m == nil {
		return nil
	}
	return m.ActorRef
}

func (m *CreateActorRequest) GetActorTemplateNamespace() string {
	if m == nil {
		return ""
	}
	return m.ActorTemplateNamespace
}

func (m *CreateActorRequest) GetActorTemplateName() string {
	if m == nil {
		return ""
	}
	return m.ActorTemplateName
}

func (m *CreateActorRequest) GetWorkerSelector() *Selector {
	if m == nil {
		return nil
	}
	return m.WorkerSelector
}

type CreateActorResponse struct {
	Actor *Actor `protobuf:"bytes,1,opt,name=actor,proto3" json:"actor,omitempty"`
}

func (m *CreateActorResponse) Reset()         { *m = CreateActorResponse{} }
func (m *CreateActorResponse) String() string { return fmt.Sprintf("actor:{%v}", m.GetActor()) }
func (*CreateActorResponse) ProtoMessage()    {}

func (m *CreateActorResponse) GetActor() *Actor {
	if m == nil {
		return nil
	}
	return m.Actor
}

type SuspendActorRequest struct {
	Actor *ObjectRef `protobuf:"bytes,1,opt,name=actor,proto3" json:"actor,omitempty"`
}

func (m *SuspendActorRequest) Reset()         { *m = SuspendActorRequest{} }
func (m *SuspendActorRequest) String() string { return fmt.Sprintf("actor:{%v}", m.GetActor()) }
func (*SuspendActorRequest) ProtoMessage()    {}

func (m *SuspendActorRequest) GetActor() *ObjectRef {
	if m == nil {
		return nil
	}
	return m.Actor
}

type SuspendActorResponse struct {
	Actor *Actor `protobuf:"bytes,1,opt,name=actor,proto3" json:"actor,omitempty"`
}

func (m *SuspendActorResponse) Reset()         { *m = SuspendActorResponse{} }
func (m *SuspendActorResponse) String() string { return fmt.Sprintf("actor:{%v}", m.GetActor()) }
func (*SuspendActorResponse) ProtoMessage()    {}

func (m *SuspendActorResponse) GetActor() *Actor {
	if m == nil {
		return nil
	}
	return m.Actor
}

type PauseActorRequest struct {
	Actor *ObjectRef `protobuf:"bytes,1,opt,name=actor,proto3" json:"actor,omitempty"`
}

func (m *PauseActorRequest) Reset()         { *m = PauseActorRequest{} }
func (m *PauseActorRequest) String() string { return fmt.Sprintf("actor:{%v}", m.GetActor()) }
func (*PauseActorRequest) ProtoMessage()    {}

func (m *PauseActorRequest) GetActor() *ObjectRef {
	if m == nil {
		return nil
	}
	return m.Actor
}

type PauseActorResponse struct {
	Actor *Actor `protobuf:"bytes,1,opt,name=actor,proto3" json:"actor,omitempty"`
}

func (m *PauseActorResponse) Reset()         { *m = PauseActorResponse{} }
func (m *PauseActorResponse) String() string { return fmt.Sprintf("actor:{%v}", m.GetActor()) }
func (*PauseActorResponse) ProtoMessage()    {}

func (m *PauseActorResponse) GetActor() *Actor {
	if m == nil {
		return nil
	}
	return m.Actor
}

type ResumeActorRequest struct {
	Actor *ObjectRef `protobuf:"bytes,1,opt,name=actor,proto3" json:"actor,omitempty"`
}

func (m *ResumeActorRequest) Reset()         { *m = ResumeActorRequest{} }
func (m *ResumeActorRequest) String() string { return fmt.Sprintf("actor:{%v}", m.GetActor()) }
func (*ResumeActorRequest) ProtoMessage()    {}

func (m *ResumeActorRequest) GetActor() *ObjectRef {
	if m == nil {
		return nil
	}
	return m.Actor
}

type ResumeActorResponse struct {
	Actor   *Actor `protobuf:"bytes,1,opt,name=actor,proto3" json:"actor,omitempty"`
	Resumed bool   `protobuf:"varint,2,opt,name=resumed,proto3" json:"resumed,omitempty"`
}

func (m *ResumeActorResponse) Reset()      { *m = ResumeActorResponse{} }
func (m *ResumeActorResponse) String() string {
	return fmt.Sprintf("actor:{%v} resumed:%v", m.GetActor(), m.GetResumed())
}
func (*ResumeActorResponse) ProtoMessage() {}

func (m *ResumeActorResponse) GetActor() *Actor {
	if m == nil {
		return nil
	}
	return m.Actor
}

func (m *ResumeActorResponse) GetResumed() bool {
	if m == nil {
		return false
	}
	return m.Resumed
}

// ControlClient is the client API for the upstream ateapi.Control lifecycle
// subset.
type ControlClient interface {
	GetActor(ctx context.Context, in *GetActorRequest, opts ...grpc.CallOption) (*Actor, error)
	CreateActor(ctx context.Context, in *CreateActorRequest, opts ...grpc.CallOption) (*Actor, error)
	SuspendActor(ctx context.Context, in *SuspendActorRequest, opts ...grpc.CallOption) (*SuspendActorResponse, error)
	PauseActor(ctx context.Context, in *PauseActorRequest, opts ...grpc.CallOption) (*PauseActorResponse, error)
	ResumeActor(ctx context.Context, in *ResumeActorRequest, opts ...grpc.CallOption) (*ResumeActorResponse, error)
}

type controlClient struct {
	cc grpc.ClientConnInterface
}

// NewControlClient binds the lifecycle RPCs to conn. Method paths match
// upstream ateapi.Control exactly.
func NewControlClient(cc grpc.ClientConnInterface) ControlClient {
	return &controlClient{cc}
}

func (c *controlClient) GetActor(ctx context.Context, in *GetActorRequest, opts ...grpc.CallOption) (*Actor, error) {
	out := new(GetActorResponse)
	err := c.cc.Invoke(ctx, Control_GetActor_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out.GetActor(), nil
}

func (c *controlClient) CreateActor(ctx context.Context, in *CreateActorRequest, opts ...grpc.CallOption) (*Actor, error) {
	out := new(CreateActorResponse)
	err := c.cc.Invoke(ctx, Control_CreateActor_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out.GetActor(), nil
}

func (c *controlClient) SuspendActor(ctx context.Context, in *SuspendActorRequest, opts ...grpc.CallOption) (*SuspendActorResponse, error) {
	out := new(SuspendActorResponse)
	err := c.cc.Invoke(ctx, Control_SuspendActor_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *controlClient) PauseActor(ctx context.Context, in *PauseActorRequest, opts ...grpc.CallOption) (*PauseActorResponse, error) {
	out := new(PauseActorResponse)
	err := c.cc.Invoke(ctx, Control_PauseActor_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *controlClient) ResumeActor(ctx context.Context, in *ResumeActorRequest, opts ...grpc.CallOption) (*ResumeActorResponse, error) {
	out := new(ResumeActorResponse)
	err := c.cc.Invoke(ctx, Control_ResumeActor_FullMethodName, in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ControlServer serves the lifecycle subset. Implementations embed
// UnimplementedControlServer for forward compatibility.
type ControlServer interface {
	GetActor(context.Context, *GetActorRequest) (*Actor, error)
	CreateActor(context.Context, *CreateActorRequest) (*Actor, error)
	SuspendActor(context.Context, *SuspendActorRequest) (*SuspendActorResponse, error)
	PauseActor(context.Context, *PauseActorRequest) (*PauseActorResponse, error)
	ResumeActor(context.Context, *ResumeActorRequest) (*ResumeActorResponse, error)
	mustEmbedUnimplementedControlServer()
}

// UnimplementedControlServer returns Unimplemented for every RPC.
type UnimplementedControlServer struct{}

func (UnimplementedControlServer) GetActor(context.Context, *GetActorRequest) (*Actor, error) {
	return nil, status.Error(codes.Unimplemented, "GetActor is not implemented")
}

func (UnimplementedControlServer) CreateActor(context.Context, *CreateActorRequest) (*Actor, error) {
	return nil, status.Error(codes.Unimplemented, "CreateActor is not implemented")
}

func (UnimplementedControlServer) SuspendActor(context.Context, *SuspendActorRequest) (*SuspendActorResponse, error) {
	return nil, status.Error(codes.Unimplemented, "SuspendActor is not implemented")
}

func (UnimplementedControlServer) PauseActor(context.Context, *PauseActorRequest) (*PauseActorResponse, error) {
	return nil, status.Error(codes.Unimplemented, "PauseActor is not implemented")
}

func (UnimplementedControlServer) ResumeActor(context.Context, *ResumeActorRequest) (*ResumeActorResponse, error) {
	return nil, status.Error(codes.Unimplemented, "ResumeActor is not implemented")
}

func (UnimplementedControlServer) mustEmbedUnimplementedControlServer() {}

// RegisterControlServer registers the lifecycle service on s.
func RegisterControlServer(s grpc.ServiceRegistrar, srv ControlServer) {
	s.RegisterService(&Control_ServiceDesc, srv)
}

// Control_ServiceDesc is the gRPC service descriptor for ateapi.Control's
// lifecycle subset.
var Control_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "ateapi.Control",
	HandlerType: (*ControlServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "GetActor", Handler: controlGetActorHandler},
		{MethodName: "CreateActor", Handler: controlCreateActorHandler},
		{MethodName: "SuspendActor", Handler: controlSuspendActorHandler},
		{MethodName: "PauseActor", Handler: controlPauseActorHandler},
		{MethodName: "ResumeActor", Handler: controlResumeActorHandler},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "ateapi.proto",
}

func wrapGetActor(actor *Actor, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return &GetActorResponse{Actor: actor}, nil
}

func wrapCreateActor(actor *Actor, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return &CreateActorResponse{Actor: actor}, nil
}

func controlGetActorHandler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(GetActorRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return wrapGetActor(srv.(ControlServer).GetActor(ctx, in))
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: Control_GetActor_FullMethodName}
	handler := func(ctx context.Context, req any) (any, error) {
		return wrapGetActor(srv.(ControlServer).GetActor(ctx, req.(*GetActorRequest)))
	}
	return interceptor(ctx, in, info, handler)
}

func controlCreateActorHandler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(CreateActorRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return wrapCreateActor(srv.(ControlServer).CreateActor(ctx, in))
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: Control_CreateActor_FullMethodName}
	handler := func(ctx context.Context, req any) (any, error) {
		return wrapCreateActor(srv.(ControlServer).CreateActor(ctx, req.(*CreateActorRequest)))
	}
	return interceptor(ctx, in, info, handler)
}

func controlSuspendActorHandler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(SuspendActorRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ControlServer).SuspendActor(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: Control_SuspendActor_FullMethodName}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(ControlServer).SuspendActor(ctx, req.(*SuspendActorRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func controlPauseActorHandler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(PauseActorRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ControlServer).PauseActor(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: Control_PauseActor_FullMethodName}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(ControlServer).PauseActor(ctx, req.(*PauseActorRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func controlResumeActorHandler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	in := new(ResumeActorRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(ControlServer).ResumeActor(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: Control_ResumeActor_FullMethodName}
	handler := func(ctx context.Context, req any) (any, error) {
		return srv.(ControlServer).ResumeActor(ctx, req.(*ResumeActorRequest))
	}
	return interceptor(ctx, in, info, handler)
}
