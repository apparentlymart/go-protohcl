package protohcl

import (
	"testing"

	"github.com/apparentlymart/go-protohcl/protohcl/internal/testschema"
	"github.com/google/go-cmp/cmp"
	"github.com/zclconf/go-cty-debug/ctydebug"
	"github.com/zclconf/go-cty/cty"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestObjectTypeConstraintForMessageDesc(t *testing.T) {
	tests := []struct {
		messageType string
		want        cty.Type
		wantErr     string
	}{
		{
			"WithStringAttr",
			cty.Object(map[string]cty.Type{
				"name": cty.String,
			}),
			``,
		},
		{
			"WithRawDynamicAttr",
			cty.Object(map[string]cty.Type{
				"raw": cty.DynamicPseudoType,
			}),
			``,
		},
		{
			"WithNumberAttrAsInt32",
			cty.Object(map[string]cty.Type{
				"num": cty.Number,
			}),
			``,
		},
		{
			"WithNumberAttrAsString",
			cty.Object(map[string]cty.Type{
				"num": cty.Number,
			}),
			``,
		},
		{
			"WithBoolAttr",
			cty.Object(map[string]cty.Type{
				"do_the_thing": cty.Bool,
			}),
			``,
		},
		{
			"WithStringListAttr",
			cty.Object(map[string]cty.Type{
				"names": cty.List(cty.String),
			}),
			``,
		},
		{
			"WithStringSetAttr",
			cty.Object(map[string]cty.Type{
				"names": cty.Set(cty.String),
			}),
			``,
		},
		{
			"WithStringMapAttr",
			cty.Object(map[string]cty.Type{
				"names": cty.Map(cty.String),
			}),
			``,
		},
		{
			"WithFlattenStringAttr",
			cty.Object(map[string]cty.Type{
				"name":    cty.String,
				"species": cty.String,
			}),
			``,
		},
		{
			"WithNestedFlattenStringAttr",
			cty.Object(map[string]cty.Type{
				"name":    cty.String,
				"species": cty.String,
				"breed":   cty.String,
			}),
			``,
		},
		{
			"WithOneBlockLabel",
			cty.Object(map[string]cty.Type{
				"name":     cty.String,
				"nickname": cty.String,
			}),
			``,
		},
		{
			"WithTwoBlockLabels",
			cty.Object(map[string]cty.Type{
				"type":     cty.String,
				"name":     cty.String,
				"nickname": cty.String,
			}),
			``,
		},
	}

	for _, test := range tests {
		t.Run(test.messageType, func(t *testing.T) {
			desc := testschema.File_testschema_proto.Messages().ByName(protoreflect.Name(test.messageType))

			got, err := ObjectTypeConstraintForMessageDesc(desc)

			if test.wantErr != "" {
				if err == nil {
					t.Fatalf("unexpected success\nwant error: %s", test.wantErr)
				}
				if err.Error() != test.wantErr {
					t.Fatalf("wrong error\ngot error:  %s\nwant error: %s", err.Error(), test.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error\ngot error: %s", err.Error())
			}

			if diff := cmp.Diff(got, test.want, ctydebug.CmpOptions); diff != "" {
				t.Errorf("wrong result\n%s", diff)
			}
		})
	}
}
