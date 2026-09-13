// Copyright 2020-2026 ONDEWO GmbH
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Everything in this file names the ONDEWO VTSI API specifically: its services, its messages, its
// enums. It is the ONLY product-specific file of the suite - generated_code_test.go and
// auth_test.go are product agnostic.
//
// VTSI is the widest of the composite products: ondewo-vtsi-api vendors the NLU, QA, S2T, SIP and
// T2S protos, so this module ships SIX go packages and exposes 23 services - its own
// ondewo.vtsi.{Calls,Logs,Projects} plus the sixteen ondewo.nlu.*, ondewo.qa.QA,
// ondewo.s2t.Speech2Text, ondewo.sip.Sip and ondewo.t2s.Text2Speech. A consumer that drives a
// telephony call and then reads its NLU session over the same client is the normal case, so all
// six packages are part of this client's surface and all six are pinned here.
package tests

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	nlu "github.com/ondewo/ondewo-vtsi-client-go/v8/api/ondewo/nlu"
	qa "github.com/ondewo/ondewo-vtsi-client-go/v8/api/ondewo/qa"
	s2t "github.com/ondewo/ondewo-vtsi-client-go/v8/api/ondewo/s2t"
	sip "github.com/ondewo/ondewo-vtsi-client-go/v8/api/ondewo/sip"
	t2s "github.com/ondewo/ondewo-vtsi-client-go/v8/api/ondewo/t2s"
	vtsi "github.com/ondewo/ondewo-vtsi-client-go/v8/api/ondewo/vtsi"
)

// protoFileCount is the number of .proto files below ondewo-vtsi-api/ondewo that the compiler
// consumed. Every one of them has to end up in the global descriptor registry when this package
// is linked; a proto that silently stopped being compiled is otherwise invisible until a
// consumer misses a type.
const protoFileCount = 25

// services is every gRPC service this product exposes, keyed by the fully qualified proto name
// the ServiceDesc must declare.
var services = map[string]*grpc.ServiceDesc{
	"ondewo.vtsi.Calls":            &vtsi.Calls_ServiceDesc,
	"ondewo.vtsi.Logs":             &vtsi.Logs_ServiceDesc,
	"ondewo.vtsi.Projects":         &vtsi.Projects_ServiceDesc,
	"ondewo.nlu.Agents":            &nlu.Agents_ServiceDesc,
	"ondewo.nlu.AiServices":        &nlu.AiServices_ServiceDesc,
	"ondewo.nlu.CcaiProjects":      &nlu.CcaiProjects_ServiceDesc,
	"ondewo.nlu.Contexts":          &nlu.Contexts_ServiceDesc,
	"ondewo.nlu.EntityTypes":       &nlu.EntityTypes_ServiceDesc,
	"ondewo.nlu.Intents":           &nlu.Intents_ServiceDesc,
	"ondewo.nlu.LlmEvaluations":    &nlu.LlmEvaluations_ServiceDesc,
	"ondewo.nlu.Operations":        &nlu.Operations_ServiceDesc,
	"ondewo.nlu.ProjectRoles":      &nlu.ProjectRoles_ServiceDesc,
	"ondewo.nlu.ProjectStatistics": &nlu.ProjectStatistics_ServiceDesc,
	"ondewo.nlu.Rags":              &nlu.Rags_ServiceDesc,
	"ondewo.nlu.ServerStatistics":  &nlu.ServerStatistics_ServiceDesc,
	"ondewo.nlu.Sessions":          &nlu.Sessions_ServiceDesc,
	"ondewo.nlu.Users":             &nlu.Users_ServiceDesc,
	"ondewo.nlu.Utilities":         &nlu.Utilities_ServiceDesc,
	"ondewo.nlu.Webhook":           &nlu.Webhook_ServiceDesc,
	"ondewo.qa.QA":                 &qa.QA_ServiceDesc,
	"ondewo.s2t.Speech2Text":       &s2t.Speech2Text_ServiceDesc,
	"ondewo.sip.Sip":               &sip.Sip_ServiceDesc,
	"ondewo.t2s.Text2Speech":       &t2s.Text2Speech_ServiceDesc,
}

// clientConstructors is the generated New<Service>Client of every service above. A client SDK
// that compiles but whose constructors are missing is useless, and the two generators that
// produce them (protoc-gen-go, protoc-gen-go-grpc) can disagree - so both halves are listed.
var clientConstructors = map[string]func(grpc.ClientConnInterface) any{
	"ondewo.vtsi.Calls":            func(cc grpc.ClientConnInterface) any { return vtsi.NewCallsClient(cc) },
	"ondewo.vtsi.Logs":             func(cc grpc.ClientConnInterface) any { return vtsi.NewLogsClient(cc) },
	"ondewo.vtsi.Projects":         func(cc grpc.ClientConnInterface) any { return vtsi.NewProjectsClient(cc) },
	"ondewo.nlu.Agents":            func(cc grpc.ClientConnInterface) any { return nlu.NewAgentsClient(cc) },
	"ondewo.nlu.AiServices":        func(cc grpc.ClientConnInterface) any { return nlu.NewAiServicesClient(cc) },
	"ondewo.nlu.CcaiProjects":      func(cc grpc.ClientConnInterface) any { return nlu.NewCcaiProjectsClient(cc) },
	"ondewo.nlu.Contexts":          func(cc grpc.ClientConnInterface) any { return nlu.NewContextsClient(cc) },
	"ondewo.nlu.EntityTypes":       func(cc grpc.ClientConnInterface) any { return nlu.NewEntityTypesClient(cc) },
	"ondewo.nlu.Intents":           func(cc grpc.ClientConnInterface) any { return nlu.NewIntentsClient(cc) },
	"ondewo.nlu.LlmEvaluations":    func(cc grpc.ClientConnInterface) any { return nlu.NewLlmEvaluationsClient(cc) },
	"ondewo.nlu.Operations":        func(cc grpc.ClientConnInterface) any { return nlu.NewOperationsClient(cc) },
	"ondewo.nlu.ProjectRoles":      func(cc grpc.ClientConnInterface) any { return nlu.NewProjectRolesClient(cc) },
	"ondewo.nlu.ProjectStatistics": func(cc grpc.ClientConnInterface) any { return nlu.NewProjectStatisticsClient(cc) },
	"ondewo.nlu.Rags":              func(cc grpc.ClientConnInterface) any { return nlu.NewRagsClient(cc) },
	"ondewo.nlu.ServerStatistics":  func(cc grpc.ClientConnInterface) any { return nlu.NewServerStatisticsClient(cc) },
	"ondewo.nlu.Sessions":          func(cc grpc.ClientConnInterface) any { return nlu.NewSessionsClient(cc) },
	"ondewo.nlu.Users":             func(cc grpc.ClientConnInterface) any { return nlu.NewUsersClient(cc) },
	"ondewo.nlu.Utilities":         func(cc grpc.ClientConnInterface) any { return nlu.NewUtilitiesClient(cc) },
	"ondewo.nlu.Webhook":           func(cc grpc.ClientConnInterface) any { return nlu.NewWebhookClient(cc) },
	"ondewo.qa.QA":                 func(cc grpc.ClientConnInterface) any { return qa.NewQAClient(cc) },
	"ondewo.s2t.Speech2Text":       func(cc grpc.ClientConnInterface) any { return s2t.NewSpeech2TextClient(cc) },
	"ondewo.sip.Sip":               func(cc grpc.ClientConnInterface) any { return sip.NewSipClient(cc) },
	"ondewo.t2s.Text2Speech":       func(cc grpc.ClientConnInterface) any { return t2s.NewText2SpeechClient(cc) },
}

// expectedMethods pins RPCs by name. The descriptor cross-check in generated_code_test.go proves
// the two generators agree with each other; it cannot notice an RPC that was renamed upstream,
// because both halves would be renamed together. These are spelled out so that a rename is a
// failing test rather than a silently broken consumer.
var expectedMethods = map[string][]string{
	"ondewo.vtsi.Calls": {
		"StartCaller", "StartCallers", "ListCallers", "GetCaller", "StopCaller",
		"StartListener", "StopListener", "ListListeners", "GetListener",
		"StartScheduledCaller", "CancelScheduledCaller",
		"StopCall", "StopAllCalls", "TransferCall", "GetCall", "ListCalls",
	},
	// StreamCallLogs is server streaming, so it lives in ServiceDesc.Streams rather than
	// .Methods - the lookup has to consider both.
	"ondewo.vtsi.Logs": {
		"ListCallLogs", "StreamCallLogs", "GetCallLogStream", "ListCallLogStreams", "DeleteCallLogs",
	},
	"ondewo.vtsi.Projects": {
		"CreateVtsiProject", "GetVtsiProject", "UpdateVtsiProject", "DeleteVtsiProject",
		"DeployVtsiProject", "UndeployVtsiProject", "ListVtsiProjects",
	},
	"ondewo.sip.Sip":      {"SipStartCall", "SipEndCall", "SipTransferCall", "SipGetSipStatus"},
	"ondewo.qa.QA":        {"GetAnswer", "GetServerState", "ListProjectIds"},
	"ondewo.nlu.Sessions": {"DetectIntent", "StreamingDetectIntent", "ListSessions", "GetSession"},
}

// TestMessageRoundTripsThroughTheWire is the core assertion about generated message code: a value
// built in go, serialized and parsed back is the same value. CallLogEntry is the richest message
// this product owns: a 64 bit scalar, a well-known Timestamp, a bool, two enums of its own package
// and one enum imported from the vendored NLU protos - so a generator that mis-numbers a field or
// drops the cross-package import fails here.
func TestMessageRoundTripsThroughTheWire(t *testing.T) {
	t.Parallel()

	original := &vtsi.CallLogEntry{
		Seq:              1 << 40,
		Timestamp:        timestamppb.New(referenceTime),
		TimestampIsExact: true,
		Level:            nlu.LogSeverity_LOG_SEVERITY_WARNING,
		Message:          "call 4c9a1f2e handed over to a human agent",
		ContainerId:      "6b1d0c3e0f3a",
		ContainerName:    "ondewo-sip-1",
		LogSource:        vtsi.LogSource_LOG_SOURCE_CSI,
		Channel:          vtsi.LogStreamChannel_LOG_STREAM_CHANNEL_STDERR,
		Emitter:          "conversation:handover:412",
	}

	wire, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("proto.Marshal(%T) failed: %v", original, err)
	}
	if len(wire) == 0 {
		t.Fatal("proto.Marshal produced 0 bytes for a fully populated message")
	}

	parsed := &vtsi.CallLogEntry{}
	if err := proto.Unmarshal(wire, parsed); err != nil {
		t.Fatalf("proto.Unmarshal failed: %v", err)
	}

	if !proto.Equal(original, parsed) {
		t.Fatalf("round trip changed the message:\n original = %v\n  parsed = %v", original, parsed)
	}
	if got, want := parsed.GetSeq(), int64(1<<40); got != want {
		t.Errorf("int64 after round trip = %d, want %d", got, want)
	}
	if got, want := parsed.GetTimestamp().AsTime().UTC(), referenceTime.UTC(); !got.Equal(want) {
		t.Errorf("timestamp after round trip = %v, want %v", got, want)
	}
	if got, want := parsed.GetLevel(), nlu.LogSeverity_LOG_SEVERITY_WARNING; got != want {
		t.Errorf("cross-package enum after round trip = %v, want %v", got, want)
	}
	if got, want := parsed.GetChannel(), vtsi.LogStreamChannel_LOG_STREAM_CHANNEL_STDERR; got != want {
		t.Errorf("enum after round trip = %v, want %v", got, want)
	}
}

// TestProto3ExplicitPresenceSurvivesTheWire guards the field kind that generators get wrong: a
// proto3 `optional` scalar has to keep the difference between "set to the zero value" and "not
// set". The angular target of the same compiler lost exactly this distinction, which made a
// false/0/"" unsendable; protoc-gen-go models it as a pointer, and this asserts it stays that way.
//
// ListCallLogsRequest.oldest_first is the case that would hurt most: `false` is a meaningful
// request (return the newest window) and so is "not set" (apply the server default).
func TestProto3ExplicitPresenceSurvivesTheWire(t *testing.T) {
	t.Parallel()

	t.Run("zero value set explicitly is transmitted", func(t *testing.T) {
		t.Parallel()

		wire, err := proto.Marshal(&vtsi.ListCallLogsRequest{OldestFirst: proto.Bool(false)})
		if err != nil {
			t.Fatalf("proto.Marshal failed: %v", err)
		}
		if len(wire) == 0 {
			t.Fatal("an explicitly set zero value was dropped from the wire - proto3 presence is lost")
		}

		parsed := &vtsi.ListCallLogsRequest{}
		if err := proto.Unmarshal(wire, parsed); err != nil {
			t.Fatalf("proto.Unmarshal failed: %v", err)
		}
		if parsed.OldestFirst == nil {
			t.Fatal("OldestFirst is nil after the round trip, want a pointer to false")
		}
		if got := *parsed.OldestFirst; got {
			t.Errorf("OldestFirst = %v, want false", got)
		}
	})

	t.Run("unset stays unset", func(t *testing.T) {
		t.Parallel()

		wire, err := proto.Marshal(&vtsi.ListCallLogsRequest{VtsiProjectName: "projects/4c9a1f2e/project"})
		if err != nil {
			t.Fatalf("proto.Marshal failed: %v", err)
		}

		parsed := &vtsi.ListCallLogsRequest{}
		if err := proto.Unmarshal(wire, parsed); err != nil {
			t.Fatalf("proto.Unmarshal failed: %v", err)
		}
		if parsed.OldestFirst != nil {
			t.Errorf("OldestFirst = %v after a round trip that never set it, want nil", *parsed.OldestFirst)
		}
		if parsed.MaxLines != nil {
			t.Errorf("MaxLines = %v after a round trip that never set it, want nil", *parsed.MaxLines)
		}
	})
}

// TestEnumZeroValueIsTheUnspecifiedMember checks the member every proto3 enum should have at 0 and
// the name maps generated beside it. A zero value that is a real choice rather than "unspecified"
// is unrequestable in several of the other clients of this API - ondewo.vtsi.CallView is the
// textbook case (its zero member MINIMUM is a view a caller really asks for), so it is pinned by
// name here instead of being wished away.
func TestEnumZeroValueIsTheUnspecifiedMember(t *testing.T) {
	t.Parallel()

	var zero vtsi.LogSource

	if zero != vtsi.LogSource_LOG_SOURCE_UNSPECIFIED {
		t.Errorf("zero value of LogSource = %v, want LOG_SOURCE_UNSPECIFIED", zero)
	}
	if got, want := zero.String(), "LOG_SOURCE_UNSPECIFIED"; got != want {
		t.Errorf("LogSource(0).String() = %q, want %q", got, want)
	}
	if got, want := vtsi.LogSource_name[0], "LOG_SOURCE_UNSPECIFIED"; got != want {
		t.Errorf("LogSource_name[0] = %q, want %q", got, want)
	}
	if got, want := vtsi.LogSource_value["LOG_SOURCE_CSI"], int32(vtsi.LogSource_LOG_SOURCE_CSI); got != want {
		t.Errorf("LogSource_value[LOG_SOURCE_CSI] = %d, want %d", got, want)
	}
	if got, want := int32(vtsi.LogSource_LOG_SOURCE_CSI), int32(2); got != want {
		t.Errorf("LOG_SOURCE_CSI = %d, want %d", got, want)
	}

	// CallView has no *_UNSPECIFIED member: 0 is MINIMUM, a view callers really request. A client
	// that cannot transmit the enum zero value cannot ask for it at all - which is exactly the bug
	// the angular target of this compiler once shipped.
	var view vtsi.CallView
	if view != vtsi.CallView_MINIMUM {
		t.Errorf("zero value of CallView = %v, want MINIMUM", view)
	}
	if got, want := vtsi.CallView_name[0], "MINIMUM"; got != want {
		t.Errorf("CallView_name[0] = %q, want %q", got, want)
	}
}

// TestUnmarshalRejectsTruncatedInput asserts the generated message reports a parse error instead
// of accepting a malformed payload: field 1 (`vtsi_project_name`) is announced as 5 bytes long but
// only 1 follows.
func TestUnmarshalRejectsTruncatedInput(t *testing.T) {
	t.Parallel()

	if err := proto.Unmarshal([]byte{0x0a, 0x05, 'a'}, &vtsi.ListCallLogsRequest{}); err == nil {
		t.Fatal("proto.Unmarshal accepted a truncated payload, want an error")
	}
}

// logsServer is a fake ONDEWO VTSI server: it answers ListCallLogs and inherits the
// "unimplemented" behaviour of the generated base type for every other RPC of the service.
type logsServer struct {
	vtsi.UnimplementedLogsServer
}

func (logsServer) ListCallLogs(_ context.Context, req *vtsi.ListCallLogsRequest) (*vtsi.ListCallLogsResponse, error) {
	return &vtsi.ListCallLogsResponse{
		LogEntries: []*vtsi.CallLogEntry{{
			Seq:       7,
			Message:   "listing logs of " + req.GetVtsiProjectName(),
			LogSource: vtsi.LogSource_LOG_SOURCE_SIP,
		}},
		MaxAvailableSeq: 7,
	}, nil
}

// TestUnaryRPCRoundTripsOverAnInProcessServer drives the generated client stub, the generated
// server stub and the generated ServiceDesc against each other over a real gRPC connection - the
// request is marshalled, routed by the method name baked into the stub, and the response is
// parsed back. Nothing here is mocked except the transport, which is in memory.
func TestUnaryRPCRoundTripsOverAnInProcessServer(t *testing.T) {
	t.Parallel()

	conn := dialInProcess(t, nil, func(srv *grpc.Server) {
		vtsi.RegisterLogsServer(srv, logsServer{})
	})
	client := vtsi.NewLogsClient(conn)

	const projectName = "projects/4c9a1f2e/project"
	response, err := client.ListCallLogs(t.Context(), &vtsi.ListCallLogsRequest{VtsiProjectName: projectName})
	if err != nil {
		t.Fatalf("ListCallLogs failed: %v", err)
	}

	if got, want := len(response.GetLogEntries()), 1; got != want {
		t.Fatalf("response carried %d log entries, want %d", got, want)
	}
	if got, want := response.GetLogEntries()[0].GetMessage(), "listing logs of "+projectName; got != want {
		t.Errorf("response message = %q, want %q", got, want)
	}
	if got, want := response.GetLogEntries()[0].GetLogSource(), vtsi.LogSource_LOG_SOURCE_SIP; got != want {
		t.Errorf("response log source = %v, want %v", got, want)
	}
}

// TestUnimplementedMethodIsReportedAsUnimplemented pins the other half of the generated server
// contract: an RPC the server does not implement must come back as codes.Unimplemented, not as a
// routing failure or a panic. It also proves the method is routed at all - a method missing from
// the ServiceDesc would surface as a different code.
func TestUnimplementedMethodIsReportedAsUnimplemented(t *testing.T) {
	t.Parallel()

	conn := dialInProcess(t, nil, func(srv *grpc.Server) {
		vtsi.RegisterLogsServer(srv, logsServer{})
	})
	client := vtsi.NewLogsClient(conn)

	_, err := client.DeleteCallLogs(t.Context(), &vtsi.DeleteCallLogsRequest{})
	if got := status.Code(err); got != codes.Unimplemented {
		t.Fatalf("DeleteCallLogs returned code %v (err = %v), want %v", got, err, codes.Unimplemented)
	}
}
