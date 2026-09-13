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

// This file is PRODUCT AGNOSTIC: it drives the tables of api_surface_test.go and names no ONDEWO
// service, message or enum of its own, so it copies unchanged into the sibling go clients.
package tests

import (
	"context"
	"net"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// referenceTime is an arbitrary but fixed instant, so a round trip through a Timestamp compares
// exactly instead of racing the clock.
var referenceTime = time.Date(2026, time.March, 14, 15, 9, 26, 0, time.UTC)

// dialInProcess starts a gRPC server on an in-memory listener, registers the caller's service on
// it and returns a connection to it. There is no port, no network and no fixture server: the
// generated client and server stubs talk to each other over a real HTTP/2 connection in this
// process, and everything is torn down when the test ends.
func dialInProcess(
	t *testing.T,
	serverOptions []grpc.ServerOption,
	register func(*grpc.Server),
	opts ...grpc.DialOption,
) *grpc.ClientConn {
	t.Helper()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(serverOptions...)
	register(server)

	served := make(chan struct{})
	go func() {
		defer close(served)
		// A Serve error after Stop is the normal shutdown path, so it is not reported.
		_ = server.Serve(listener)
	}()

	dialOptions := append([]grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}, opts...)

	// `passthrough:///` keeps the resolver away from DNS - the target is handled by the dialer.
	conn, err := grpc.NewClient("passthrough:///ondewo-in-process", dialOptions...)
	if err != nil {
		t.Fatalf("grpc.NewClient against the in-process listener failed: %v", err)
	}

	t.Cleanup(func() {
		_ = conn.Close()
		server.Stop()
		<-served
	})

	return conn
}

// methodNames is every RPC of a generated ServiceDesc. Streaming RPCs are listed separately by
// protoc-gen-go-grpc, so both halves have to be read to see the whole service.
func methodNames(desc *grpc.ServiceDesc) []string {
	names := make([]string, 0, len(desc.Methods)+len(desc.Streams))
	for _, method := range desc.Methods {
		names = append(names, method.MethodName)
	}
	for _, stream := range desc.Streams {
		names = append(names, stream.StreamName)
	}
	sort.Strings(names)

	return names
}

// TestServiceDescriptorsMatchTheProtoDescriptors cross-checks the output of the two generators
// against each other: the grpc.ServiceDesc comes from protoc-gen-go-grpc, the proto descriptor in
// the global registry from protoc-gen-go. They are produced by separate plugins from the same
// .proto, so an RPC present in only one of them is a generator or compile-script defect - and one
// that a plain `go build` cannot see, because both halves compile on their own.
func TestServiceDescriptorsMatchTheProtoDescriptors(t *testing.T) {
	t.Parallel()

	if len(services) == 0 {
		t.Fatal("the services table is empty - api_surface_test.go has to list this product's services")
	}

	for fullName, serviceDesc := range services {
		t.Run(fullName, func(t *testing.T) {
			t.Parallel()

			if serviceDesc.ServiceName != fullName {
				t.Fatalf("ServiceDesc.ServiceName = %q, want %q", serviceDesc.ServiceName, fullName)
			}

			descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(fullName))
			if err != nil {
				t.Fatalf("service %q is not in the global descriptor registry: %v", fullName, err)
			}
			serviceDescriptor, ok := descriptor.(protoreflect.ServiceDescriptor)
			if !ok {
				t.Fatalf("%q resolves to %T, want a protoreflect.ServiceDescriptor", fullName, descriptor)
			}

			methods := serviceDescriptor.Methods()
			fromProto := make([]string, 0, methods.Len())
			for i := 0; i < methods.Len(); i++ {
				fromProto = append(fromProto, string(methods.Get(i).Name()))
			}
			sort.Strings(fromProto)

			fromGRPC := methodNames(serviceDesc)
			if len(fromGRPC) == 0 {
				t.Fatalf("service %q has no RPCs in its ServiceDesc", fullName)
			}
			if strings.Join(fromGRPC, ",") != strings.Join(fromProto, ",") {
				t.Errorf(
					"the two generators disagree about the RPCs of %q:\n  protoc-gen-go-grpc: %v\n  protoc-gen-go:      %v",
					fullName, fromGRPC, fromProto,
				)
			}
		})
	}
}

// TestExpectedServiceMethodsExist pins RPC names that consumers of this client call, so an
// upstream rename shows up here instead of in a consumer's build.
func TestExpectedServiceMethodsExist(t *testing.T) {
	t.Parallel()

	if len(expectedMethods) == 0 {
		t.Fatal("the expectedMethods table is empty - api_surface_test.go has to pin this product's RPCs")
	}

	for fullName, wanted := range expectedMethods {
		t.Run(fullName, func(t *testing.T) {
			t.Parallel()

			serviceDesc, ok := services[fullName]
			if !ok {
				t.Fatalf("%q is pinned in expectedMethods but missing from the services table", fullName)
			}

			present := make(map[string]bool, len(serviceDesc.Methods)+len(serviceDesc.Streams))
			for _, name := range methodNames(serviceDesc) {
				present[name] = true
			}

			for _, method := range wanted {
				if !present[method] {
					t.Errorf("service %q has no RPC %q - it has %v", fullName, method, methodNames(serviceDesc))
				}
			}
		})
	}
}

// TestServiceClientConstructorsBindToAConnection calls every generated New<Service>Client against
// a live connection and asserts it yields a usable client. The connection is the in-process one,
// so this needs no server for the services it does not register and no network at all.
func TestServiceClientConstructorsBindToAConnection(t *testing.T) {
	t.Parallel()

	if len(clientConstructors) != len(services) {
		t.Fatalf(
			"clientConstructors has %d entries but services has %d - every service needs its constructor listed",
			len(clientConstructors), len(services),
		)
	}

	conn := dialInProcess(t, nil, func(*grpc.Server) {})

	for fullName, construct := range clientConstructors {
		t.Run(fullName, func(t *testing.T) {
			t.Parallel()

			if _, ok := services[fullName]; !ok {
				t.Fatalf("%q has a constructor but is missing from the services table", fullName)
			}
			if client := construct(conn); client == nil {
				t.Fatalf("the generated constructor of %q returned nil", fullName)
			}
		})
	}
}

// TestEveryGeneratedUnaryStubReachesTheServer calls every unary RPC of every service - several
// hundred of them - against an in-process server with no service registered, and requires each to
// come back as codes.Unimplemented. Reaching that answer means the generated stub marshalled its
// request, built a method name the transport accepted and parsed the status back; a stub wired to
// the wrong request type or a missing codec fails here instead of in a consumer's production call.
// The methods are found by their generated signature rather than listed, so a new RPC upstream is
// covered the moment it is generated.
//
// Streaming RPCs are skipped: their generated stub returns a stream, and opening one against an
// unregistered service is answered on the first Recv rather than at call time, which is a
// different assertion than the one this test makes.
func TestEveryGeneratedUnaryStubReachesTheServer(t *testing.T) {
	t.Parallel()

	var (
		contextType    = reflect.TypeOf((*context.Context)(nil)).Elem()
		callOptionType = reflect.TypeOf((*grpc.CallOption)(nil)).Elem()
		errorType      = reflect.TypeOf((*error)(nil)).Elem()
	)

	// No service is registered, so every method of every service is unknown to this server.
	conn := dialInProcess(t, nil, func(*grpc.Server) {})

	called := 0
	for fullName, construct := range clientConstructors {
		client := reflect.ValueOf(construct(conn))

		for i := range client.NumMethod() {
			name := client.Type().Method(i).Name
			method := client.Method(i)
			signature := method.Type()

			isUnaryRPC := signature.NumIn() == 3 &&
				signature.IsVariadic() &&
				signature.In(0) == contextType &&
				signature.In(2).Elem() == callOptionType &&
				signature.In(1).Kind() == reflect.Pointer &&
				signature.In(1).Elem().Kind() == reflect.Struct &&
				signature.NumOut() == 2 &&
				signature.Out(0).Kind() == reflect.Pointer &&
				signature.Out(1) == errorType
			if !isUnaryRPC {
				continue
			}

			request := reflect.New(signature.In(1).Elem())
			results := method.Call([]reflect.Value{reflect.ValueOf(t.Context()), request})

			err, _ := results[1].Interface().(error)
			if got := status.Code(err); got != codes.Unimplemented {
				t.Errorf("%s/%s returned code %v (err = %v), want %v", fullName, name, got, err, codes.Unimplemented)
			}
			called++
		}
	}

	if called == 0 {
		t.Fatal("no unary RPC was called - the signature match found nothing, which cannot be right")
	}
	t.Logf("exercised %d generated unary client stubs across %d services", called, len(clientConstructors))
}

// TestEveryProtoFileIsRegistered asserts the descriptors of all compiled .proto files are linked
// into the binary. A file that stopped being compiled - a new proto the compile script's glob
// misses, say - leaves the module building fine and the type simply absent.
func TestEveryProtoFileIsRegistered(t *testing.T) {
	t.Parallel()

	var registered []string
	protoregistry.GlobalFiles.RangeFiles(func(file protoreflect.FileDescriptor) bool {
		if strings.HasPrefix(file.Path(), "ondewo/") {
			registered = append(registered, file.Path())
		}

		return true
	})
	sort.Strings(registered)

	if len(registered) != protoFileCount {
		t.Fatalf("%d ondewo/**.proto files are registered, want %d:\n%v", len(registered), protoFileCount, registered)
	}

	for _, path := range registered {
		file, err := protoregistry.GlobalFiles.FindFileByPath(path)
		if err != nil {
			t.Fatalf("%q ranged over but not resolvable: %v", path, err)
		}
		if got := string(file.Package()); !strings.HasPrefix(got, "ondewo.") {
			t.Errorf("%q declares proto package %q, want an ondewo.* package", path, got)
		}
		if file.Syntax() != protoreflect.Proto3 {
			t.Errorf("%q is %v, want proto3", path, file.Syntax())
		}
	}
}
