package proto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Phase 1 deprecation contract for GetUserViaKeycloakId (issue #97).
// Asserts the proto-level deprecation flags that downstream consumers
// (generated code, descriptors, linters) actually observe. The OpenAPI
// surface is asserted in openapi/openapi_test.go.
func TestKeycloakIdDeprecationContract(t *testing.T) {
	svc := File_milpacs_proto.Services().ByName("MilpacService")
	require.NotNil(t, svc, "MilpacService missing from descriptor")

	rpc := svc.Methods().ByName("GetUserViaKeycloakId")
	require.NotNil(t, rpc, "GetUserViaKeycloakId rpc missing")
	assert.True(t, methodDeprecated(rpc), "GetUserViaKeycloakId must be deprecated")

	for _, c := range []struct {
		msg, field string
	}{
		{"KeycloakIdRequest", "keycloak_id"},
		{"Profile", "keycloak_id"},
		{"LiteProfile", "keycloak_id"},
	} {
		msg := File_milpacs_proto.Messages().ByName(protoreflect.Name(c.msg))
		require.NotNil(t, msg, "%s missing from descriptor", c.msg)
		f := msg.Fields().ByName(protoreflect.Name(c.field))
		require.NotNil(t, f, "%s.%s missing from descriptor", c.msg, c.field)
		assert.True(t, fieldDeprecated(f), "%s.%s must be deprecated", c.msg, c.field)
	}
}

func methodDeprecated(m protoreflect.MethodDescriptor) bool {
	opts, ok := m.Options().(*descriptorpb.MethodOptions)
	return ok && opts.GetDeprecated()
}

func fieldDeprecated(f protoreflect.FieldDescriptor) bool {
	opts, ok := f.Options().(*descriptorpb.FieldOptions)
	return ok && opts.GetDeprecated()
}
