package protohcl

import (
	"github.com/apparentlymart/go-protohcl/protohcl/protohclext"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// GetFieldElem returns a FieldElem that applies to the given field, which
// describes what HCL-specific behavior the field is annotated with.
//
// Returns a nil FieldElem if there is no valid HCL annotation at all.
//
// Returns an error if the field has invalid or contradictory HCL options.
func GetFieldElem(field protoreflect.FieldDescriptor) (FieldElem, error) {
	opts, ok := field.Options().(*descriptorpb.FieldOptions)
	if !ok {
		// If missing or totally invalid options then we skip this one.
		// This isn't an error because the schema didn't explicitly opt in
		// to HCL processing yet.
		return nil, nil
	}

	// These extensions are all mutually-exclusive with one another,
	// because each proto field must map to zero or one HCL schema
	// constructs.
	attrOpts := proto.GetExtension(opts, protohclext.E_Attr).(*protohclext.Attribute)
	blockOpts := proto.GetExtension(opts, protohclext.E_Block).(*protohclext.NestedBlock)
	flatten := proto.GetExtension(opts, protohclext.E_Flatten).(bool)
	labelOpts := proto.GetExtension(opts, protohclext.E_Label).(*protohclext.BlockLabel)

	switch {
	case attrOpts != nil && attrOpts.Name != "":
		if blockOpts != nil && blockOpts.TypeName != "" {
			return nil, schemaErrorf(field.FullName(), "cannot be both attribute %q and nested block type %q", attrOpts.Name, blockOpts.TypeName)
		}
		if flatten {
			return nil, schemaErrorf(field.FullName(), "cannot be attribute %q and also flatten into the current body", attrOpts.Name)
		}
		if labelOpts != nil && labelOpts.Name != "" {
			return nil, schemaErrorf(field.FullName(), "cannot be both attribute %q and block label %q", attrOpts.Name, labelOpts.Name)
		}

		return FieldAttribute{
			Name:           attrOpts.Name,
			Required:       attrOpts.Required,
			TypeExprString: attrOpts.Type,
			RawMode:        attrOpts.Raw,
		}, nil

	case blockOpts != nil && blockOpts.TypeName != "":
		if flatten {
			return nil, schemaErrorf(field.FullName(), "cannot be nested block type %q and also flatten into the current body", attrOpts.Name)
		}
		if labelOpts != nil && labelOpts.Name != "" {
			return nil, schemaErrorf(field.FullName(), "cannot be both nested block type %q and block label %q", attrOpts.Name, labelOpts.Name)
		}
		if field.Kind() != protoreflect.MessageKind {
			return nil, schemaErrorf(field.FullName(), "field representing nested block must have message type, not %s", field.Kind())
		}

		return FieldNestedBlockType{
			TypeName: blockOpts.TypeName,
			Nested:   field.Message(),
		}, nil

	case flatten:
		if labelOpts != nil && labelOpts.Name != "" {
			return nil, schemaErrorf(field.FullName(), "cannot be block label %q and also flatten into the current body", labelOpts.Name)
		}
		if field.Kind() != protoreflect.MessageKind {
			return nil, schemaErrorf(field.FullName(), "field to be flattened must have message type, not %s", field.Kind())
		}

		return FieldFlattened{
			Nested: field.Message(),
		}, nil

	case labelOpts != nil && labelOpts.Name != "":
		return FieldBlockLabel{
			Name: labelOpts.Name,
		}, nil

	default:
		// Otherwise this field isn't relevant to HCL at all, and we'll
		// totally ignore it.
		return nil, nil
	}

}

// FieldElem represents a HCL-specific behavior associated with a protobuf
// message field.
//
// This is a closed interface, meaning that the implementations in this
// package are the only possible implementations: FieldAttribute,
// FieldNestedBlockType, FieldFlattened, and FieldBlockLabel.
type FieldElem interface {
	fieldElem()
}

type FieldAttribute struct {
	Name     string
	Required bool

	TypeExprString string
	RawMode        protohclext.Attribute_RawMode
}

// TypeConstraint attempts to interpret field TypeExprString as an HCL type
// constraint expression, and then if successful returns the type constraint
// that it represents.
//
// If the field doesn't contain a valid type constraint expression then
// TypeConstraint returns error diagnostics and an invalid type.
func (fa FieldAttribute) TypeConstraint() (cty.Type, hcl.Diagnostics) {
	expr, diags := hclsyntax.ParseExpression([]byte(fa.TypeExprString), "", hcl.InitialPos)
	if diags.HasErrors() {
		return cty.DynamicPseudoType, diags
	}

	ty, moreDiags := typeexpr.TypeConstraint(expr)
	diags = append(diags, moreDiags...)
	return ty, diags
}

func (fa FieldAttribute) fieldElem() {}

type FieldNestedBlockType struct {
	TypeName string
	Nested   protoreflect.MessageDescriptor
}

func (fa FieldNestedBlockType) fieldElem() {}

type FieldFlattened struct {
	Nested protoreflect.MessageDescriptor
}

func (fa FieldFlattened) fieldElem() {}

type FieldBlockLabel struct {
	Name string
}

func (fa FieldBlockLabel) fieldElem() {}
