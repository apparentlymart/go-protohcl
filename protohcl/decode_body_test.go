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
	simpleRootDesc := fileDesc.Messages().ByName(protoreflect.Name("WithStringAttr"))
	simpleRawRootDesc := fileDesc.Messages().ByName(protoreflect.Name("WithRawDynamicAttr"))
	withNestedBlockNoLabelsSingletonDesc := fileDesc.Messages().ByName(protoreflect.Name("WithNestedBlockNoLabelsSingleton"))
	withNestedBlockOneLabelSingletonDesc := fileDesc.Messages().ByName(protoreflect.Name("WithNestedBlockOneLabelSingleton"))
	withFlattenStringAttrDesc := fileDesc.Messages().ByName(protoreflect.Name("WithFlattenStringAttr"))
	withNestedFlattenStringAttrDesc := fileDesc.Messages().ByName(protoreflect.Name("WithNestedFlattenStringAttr"))
	withBoolAttrDesc := fileDesc.Messages().ByName(protoreflect.Name("WithBoolAttr"))
	withNumberAttrAsInt32Desc := fileDesc.Messages().ByName(protoreflect.Name("WithNumberAttrAsInt32"))
	withNumberAttrAsStringDesc := fileDesc.Messages().ByName(protoreflect.Name("WithNumberAttrAsString"))
	withStringListAttrDesc := fileDesc.Messages().ByName(protoreflect.Name("WithStringListAttr"))
	withStringSetAttrDesc := fileDesc.Messages().ByName(protoreflect.Name("WithStringSetAttr"))
	withStringMapAttrDesc := fileDesc.Messages().ByName(protoreflect.Name("WithStringMapAttr"))

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
			&testschema.WithStringAttr{},
			nil,
		},
		"string attribute": {
			`
				name = "Jackson"
			`,
			simpleRootDesc,
			nil,
			&testschema.WithStringAttr{
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
			&testschema.WithStringAttr{
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
			&testschema.WithStringAttr{},
			nil,
		},
		"number attribute as int32": {
			`
				num = 64
			`,
			withNumberAttrAsInt32Desc,
			nil,
			&testschema.WithNumberAttrAsInt32{
				Num: 64,
			},
			nil,
		},
		"number attribute as int32 invalid fraction": {
			`
				num = 3.14159265358979323846264338327950288419716939937510582097494459
			`,
			withNumberAttrAsInt32Desc,
			nil,
			&testschema.WithNumberAttrAsInt32{},
			hcl.Diagnostics{
				{
					Severity: hcl.DiagError,
					Summary:  "Unsuitable attribute value",
					Detail:   "The value must be a whole number.",
					Subject: &hcl.Range{
						Filename: "test.tf",
						Start:    hcl.Pos{Line: 2, Column: 11, Byte: 11},
						End:      hcl.Pos{Line: 2, Column: 75, Byte: 75},
					},
				},
			},
		},
		"number attribute as int32 out of range": {
			`
				num = 314159265358979323846264338327950288419716939937510582097494459
			`,
			withNumberAttrAsInt32Desc,
			nil,
			&testschema.WithNumberAttrAsInt32{},
			hcl.Diagnostics{
				{
					Severity: hcl.DiagError,
					Summary:  "Unsuitable attribute value",
					Detail:   "The value must be less than or equal to 2147483647.",
					Subject: &hcl.Range{
						Filename: "test.tf",
						Start:    hcl.Pos{Line: 2, Column: 11, Byte: 11},
						End:      hcl.Pos{Line: 2, Column: 74, Byte: 74},
					},
				},
			},
		},
		"number attribute as string": {
			`
				num = 3.14159265358979323846264338327950288419716939937510582097494459
			`,
			withNumberAttrAsStringDesc,
			nil,
			&testschema.WithNumberAttrAsString{
				// We can preserve a decimal representation of the full
				// precision of the input, because we're not lowering to any
				// particular protobuf numeric type here.
				Num: "3.14159265358979323846264338327950288419716939937510582097494459",
			},
			nil,
		},
		"bool attribute true": {
			`
				do_the_thing = true
			`,
			withBoolAttrDesc,
			nil,
			&testschema.WithBoolAttr{
				// We can preserve a decimal representation of the full
				// precision of the input, because we're not lowering to any
				// particular protobuf numeric type here.
				DoTheThing: true,
			},
			nil,
		},
		"list of strings attribute": {
			`
				names = ["Jackson", "Snakob", "Rufus", "Agnes", "Jackson"]
			`,
			withStringListAttrDesc,
			nil,
			&testschema.WithStringListAttr{
				Names: []string{"Jackson", "Snakob", "Rufus", "Agnes", "Jackson"},
			},
			nil,
		},
		"set of strings attribute": {
			`
				names = ["Jackson", "Snakob", "Rufus", "Agnes", "Jackson"]
			`,
			withStringSetAttrDesc,
			nil,
			&testschema.WithStringSetAttr{
				// We lose the explicit ordering and the duplicate Jackson
				// in the conversion to set, even though the result is
				// just a list again.
				Names: []string{"Agnes", "Jackson", "Rufus", "Snakob"},
			},
			nil,
		},
		"map of strings attribute": {
			`
				names = {
					martin  = "Jackson"
					kristin = "Snakob"
					kay     = "Rufus"
				}
			`,
			withStringMapAttrDesc,
			nil,
			&testschema.WithStringMapAttr{
				Names: map[string]string{
					"martin":  "Jackson",
					"kristin": "Snakob",
					"kay":     "Rufus",
				},
			},
			nil,
		},
		"raw dynamic attribute as string": {
			`
				raw = "Hello"
			`,
			simpleRawRootDesc,
			nil,
			&testschema.WithRawDynamicAttr{
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
			&testschema.WithRawDynamicAttr{
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
			&testschema.WithRawDynamicAttr{
				// "Raw" doesn't get populated at all for null, for consistency with omitting it
			},
			nil,
		},
		"singleton block type with no labels": {
			`
				doodad {
					name = "Snakob"
				}
			`,
			withNestedBlockNoLabelsSingletonDesc,
			nil,
			&testschema.WithNestedBlockNoLabelsSingleton{
				Doodad: &testschema.WithStringAttr{
					Name: "Snakob",
				},
			},
			nil,
		},
		"singleton block type with too many labels": {
			`
				doodad "wrong" {
					name = "Snakob"
				}
			`,
			withNestedBlockNoLabelsSingletonDesc,
			nil,
			&testschema.WithNestedBlockNoLabelsSingleton{},
			hcl.Diagnostics{
				{
					Severity: hcl.DiagError,
					Summary:  "Extraneous label for doodad",
					Detail:   "No labels are expected for doodad blocks.",
					Subject: &hcl.Range{
						Filename: "test.tf",
						Start:    hcl.Pos{Line: 2, Column: 12, Byte: 12},
						End:      hcl.Pos{Line: 2, Column: 19, Byte: 19},
					},
					Context: &hcl.Range{
						Filename: "test.tf",
						Start:    hcl.Pos{Line: 2, Column: 5, Byte: 5},
						End:      hcl.Pos{Line: 2, Column: 21, Byte: 21},
					},
				},
			},
		},
		"singleton block type with too many blocks": {
			`
			doodad {
				name = "Jackson"
			}
			doodad {
				name = "Snakob"
			}
	`,
			withNestedBlockNoLabelsSingletonDesc,
			nil,
			&testschema.WithNestedBlockNoLabelsSingleton{
				Doodad: &testschema.WithStringAttr{
					Name: "Jackson",
				},
			},
			hcl.Diagnostics{
				{
					Severity: hcl.DiagError,
					Summary:  "Duplicate doodad block",
					Detail:   "There may be no more than one doodad block. Previous block declared at test.tf:2,4-10.",
					Subject: &hcl.Range{
						Filename: "test.tf",
						Start:    hcl.Pos{Line: 5, Column: 4, Byte: 42},
						End:      hcl.Pos{Line: 5, Column: 10, Byte: 48},
					},
					Context: &hcl.Range{
						Filename: "test.tf",
						Start:    hcl.Pos{Line: 5, Column: 4, Byte: 42},
						End:      hcl.Pos{Line: 5, Column: 10, Byte: 48},
					},
				},
			},
		},
		"singleton block type with one label": {
			`
				doodad "Jackson" {
					nickname = "doofus"
				}
			`,
			withNestedBlockOneLabelSingletonDesc,
			nil,
			&testschema.WithNestedBlockOneLabelSingleton{
				Doodad: &testschema.WithOneBlockLabel{
					Name:     "Jackson",
					Nickname: "doofus",
				},
			},
			nil,
		},
		"flattened message with string attribute": {
			`
				name    = "Joey"
				species = "budgerigar"
			`,
			withFlattenStringAttrDesc,
			nil,
			&testschema.WithFlattenStringAttr{
				Base: &testschema.WithStringAttr{
					Name: "Joey",
				},
				Species: "budgerigar",
			},
			nil,
		},
		"flattened message with nested flattened message": {
			`
				name    = "Snakob"
				species = "snake"
				breed   = "ball"
			`,
			withNestedFlattenStringAttrDesc,
			nil,
			&testschema.WithNestedFlattenStringAttr{
				Base: &testschema.WithFlattenStringAttr{
					Base: &testschema.WithStringAttr{
						Name: "Snakob",
					},
					Species: "snake",
				},
				Breed: "ball",
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
