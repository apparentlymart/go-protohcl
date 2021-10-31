package protohcl

import (
	"testing"

	"github.com/apparentlymart/go-protohcl/protohcl/internal/testschema"
	"github.com/google/go-cmp/cmp"
	"github.com/zclconf/go-cty-debug/ctydebug"
	"github.com/zclconf/go-cty/cty"
	"google.golang.org/protobuf/proto"
)

func TestObjectValueForMessage(t *testing.T) {
	tests := map[string]struct {
		msg     proto.Message
		want    cty.Value
		wantErr string
	}{
		"string attribute": {
			&testschema.WithStringAttr{
				Name: "Jackson",
			},
			cty.ObjectVal(map[string]cty.Value{
				"name": cty.StringVal("Jackson"),
			}),
			``,
		},
		"string attribute unset": {
			&testschema.WithStringAttr{},
			cty.ObjectVal(map[string]cty.Value{
				// This field doesn't have "presence", and so it being unset
				// is indistinguishable from it having the default value.
				"name": cty.StringVal(""),
			}),
			``,
		},
		"bool attribute true": {
			&testschema.WithBoolAttr{
				DoTheThing: true,
			},
			cty.ObjectVal(map[string]cty.Value{
				"do_the_thing": cty.True,
			}),
			``,
		},
		"bool attribute false": {
			&testschema.WithBoolAttr{
				DoTheThing: false,
			},
			cty.ObjectVal(map[string]cty.Value{
				"do_the_thing": cty.False,
			}),
			``,
		},
		"number attribute from int32": {
			&testschema.WithNumberAttrAsInt32{
				Num: 12,
			},
			cty.ObjectVal(map[string]cty.Value{
				"num": cty.NumberIntVal(12),
			}),
			``,
		},
		"number attribute from string": {
			&testschema.WithNumberAttrAsString{
				Num: "314159265358979323846264338327950288419716939937510582097494459",
			},
			cty.ObjectVal(map[string]cty.Value{
				"num": cty.MustParseNumberVal("314159265358979323846264338327950288419716939937510582097494459"),
			}),
			``,
		},
		"string list attribute": {
			&testschema.WithStringListAttr{
				Names: []string{"Jackson", "Rufus", "Agnes"},
			},
			cty.ObjectVal(map[string]cty.Value{
				"names": cty.ListVal([]cty.Value{
					cty.StringVal("Jackson"),
					cty.StringVal("Rufus"),
					cty.StringVal("Agnes"),
				}),
			}),
			``,
		},
		"string set attribute": {
			&testschema.WithStringSetAttr{
				Names: []string{"Jackson", "Rufus", "Agnes"},
			},
			cty.ObjectVal(map[string]cty.Value{
				"names": cty.SetVal([]cty.Value{
					cty.StringVal("Agnes"),
					cty.StringVal("Jackson"),
					cty.StringVal("Rufus"),
				}),
			}),
			``,
		},
		"string map attribute": {
			&testschema.WithStringMapAttr{
				Names: map[string]string{
					"Martin": "Jackson",
					"Kay":    "Rufus",
					"Jen":    "Agnes",
				},
			},
			cty.ObjectVal(map[string]cty.Value{
				"names": cty.MapVal(map[string]cty.Value{
					"Martin": cty.StringVal("Jackson"),
					"Kay":    cty.StringVal("Rufus"),
					"Jen":    cty.StringVal("Agnes"),
				}),
			}),
			``,
		},
		"raw dynamic attribute as string": {
			&testschema.WithRawDynamicAttr{
				Raw: []byte(`{"value":"hello","type":"string"}`),
			},
			cty.ObjectVal(map[string]cty.Value{
				"raw": cty.StringVal("hello"),
			}),
			``,
		},
		"raw dynamic attribute as bool": {
			&testschema.WithRawDynamicAttr{
				Raw: []byte(`{"value":true,"type":"bool"}`),
			},
			cty.ObjectVal(map[string]cty.Value{
				"raw": cty.True,
			}),
			``,
		},
		"raw dynamic attribute as null number": {
			&testschema.WithRawDynamicAttr{
				Raw: []byte(`{"value":null,"type":"number"}`),
			},
			cty.ObjectVal(map[string]cty.Value{
				"raw": cty.NullVal(cty.Number),
			}),
			``,
		},
		"raw dynamic attribute unset": {
			&testschema.WithRawDynamicAttr{},
			cty.ObjectVal(map[string]cty.Value{
				"raw": cty.NullVal(cty.DynamicPseudoType),
			}),
			``,
		},
		"raw dynamic attribute containing garbage": {
			&testschema.WithRawDynamicAttr{
				// protohcl should never produce garbage like this itself,
				// but we won't always necessarily be working with messages
				// that protohcl constructed, so we need to be resilient.
				Raw: []byte(`{invalid`),
			},
			cty.NilVal,
			`invalid encoding of dynamic value as bytes: failed to read dynamic type descriptor key: invalid character 'i'`,
		},
		"flattened nested messages": {
			&testschema.WithNestedFlattenStringAttr{
				Base: &testschema.WithFlattenStringAttr{
					Base: &testschema.WithStringAttr{
						Name: "Jackson",
					},
					Species: "dog",
				},
				Breed: "pitbull",
			},
			cty.ObjectVal(map[string]cty.Value{
				"name":    cty.StringVal("Jackson"),
				"species": cty.StringVal("dog"),
				"breed":   cty.StringVal("pitbull"),
			}),
			``,
		},
		"block message with one label": {
			&testschema.WithOneBlockLabel{
				Name:     "Jackson",
				Nickname: "doofus",
			},
			cty.ObjectVal(map[string]cty.Value{
				"name":     cty.StringVal("Jackson"),
				"nickname": cty.StringVal("doofus"),
			}),
			``,
		},
		"block message with two labels": {
			&testschema.WithTwoBlockLabels{
				Type:     "dog",
				Name:     "Jackson",
				Nickname: "doofus",
			},
			cty.ObjectVal(map[string]cty.Value{
				"name":     cty.StringVal("Jackson"),
				"type":     cty.StringVal("dog"),
				"nickname": cty.StringVal("doofus"),
			}),
			``,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := ObjectValueForMessage(test.msg)

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
