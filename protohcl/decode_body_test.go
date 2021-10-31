package protohcl

import (
	"testing"

	"github.com/apparentlymart/go-protohcl/protohcl/internal/testschema"
	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/testing/protocmp"
)

var protoCmpOpt = protocmp.Transform()

func TestDecodeBody(t *testing.T) {
	fileDesc := testschema.File_testschema_proto
	simpleRootDesc := fileDesc.Messages().ByName(protoreflect.Name("SimpleRoot"))
	simpleRawRootDesc := fileDesc.Messages().ByName(protoreflect.Name("SimpleRawRoot"))
	//rootDesc := fileDesc.Messages().ByName(protoreflect.Name("Root"))

	tests := map[string]struct {
		config    string
		desc      protoreflect.MessageDescriptor
		ctx       *hcl.EvalContext
		want      proto.Message
		wantDiags hcl.Diagnostics
	}{
		"empty": {
			``,
			simpleRootDesc,
			nil,
			&testschema.SimpleRoot{},
			nil,
		},
		"string attribute": {
			`
				name = "Jackson"
			`,
			simpleRootDesc,
			nil,
			&testschema.SimpleRoot{
				Name: "Jackson",
			},
			nil,
		},
		"string attribute with automatic type conversion": {
			`
				name = true
			`,
			simpleRootDesc,
			nil,
			&testschema.SimpleRoot{
				Name: "true",
			},
			nil,
		},
		"string attribute explicitly set to null": {
			`
				name = null
			`,
			simpleRootDesc,
			nil,
			&testschema.SimpleRoot{},
			nil,
		},
		"raw dynamic attribute as string": {
			`
				raw = "Hello"
			`,
			simpleRawRootDesc,
			nil,
			&testschema.SimpleRawRoot{
				Raw: []byte(`{"value":"Hello","type":"string"}`),
			},
			nil,
		},
		"raw dynamic attribute as number": {
			`
				raw = 2
			`,
			simpleRawRootDesc,
			nil,
			&testschema.SimpleRawRoot{
				Raw: []byte(`{"value":2,"type":"number"}`),
			},
			nil,
		},
		"raw dynamic attribute as null": {
			`
				raw = null
			`,
			simpleRawRootDesc,
			nil,
			&testschema.SimpleRawRoot{
				// "Raw" doesn't get populated at all for null, for consistency with omitting it
			},
			nil,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			f, diags := hclsyntax.ParseConfig([]byte(test.config), "test.tf", hcl.InitialPos)
			if diags.HasErrors() {
				t.Fatalf("parse error: %s", diags)
			}

			got, diags := DecodeBody(f.Body, test.desc, test.ctx)

			if diff := cmp.Diff(test.want, got, protoCmpOpt); diff != "" {
				t.Errorf("wrong result\n%s", diff)
			}
			if diff := cmp.Diff(test.wantDiags, diags); diff != "" {
				t.Errorf("wrong diagnostics\n%s", diff)
			}
		})
	}

}
