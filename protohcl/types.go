package protohcl

import (
	"fmt"

	"github.com/apparentlymart/go-protohcl/protohcl/protohclext"
	"github.com/zclconf/go-cty/cty"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ObjectTypeConstraintForMessageDesc returns the type constraint which all
// ObjectValueForMessage results for messages of the given descriptor will
// conform to.
//
// The result may be a non-exact type constraint, if the given message
// descriptor contains any raw fields which themselves have non-exact type
// constraints.
//
// ObjectTypeConstraintForMessageDesc will return an error if any HCL
// options in the given descriptor are invalid, so this function can also be
// useful to validate that a particular message descriptor is suitable for
// conversion to a HCL objects.
func ObjectTypeConstraintForMessageDesc(desc protoreflect.MessageDescriptor) (cty.Type, error) {
	atys := make(map[string]cty.Type)
	err := buildObjectTypeAtysForMessageDesc(desc, atys)
	if err != nil {
		return cty.NilType, err
	}
	return cty.Object(atys), nil
}

func buildObjectTypeAtysForMessageDesc(desc protoreflect.MessageDescriptor, atys map[string]cty.Type) error {
	fields := desc.Fields()

	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)

		elem, err := GetFieldElem(field)
		if err != nil {
			return err
		}
		if elem == nil {
			continue // field is not relevant to HCL
		}

		switch elem := elem.(type) {
		case FieldAttribute:
			aty, diags := elem.TypeConstraint()
			if diags.HasErrors() {
				return schemaErrorf(field.FullName(), "invalid type constraint expression")
			}
			atys[elem.Name] = aty

		case FieldNestedBlockType:
			nestedTy, err := ObjectTypeConstraintForMessageDesc(elem.Nested)
			if err != nil {
				return err
			}
			switch elem.CollectionKind {
			case protohclext.NestedBlock_AUTO:
				// AUTO always indicates single mode in the GetFieldElem
				// response, so we'll just pass through the nested message type.
				atys[elem.TypeName] = nestedTy

			case protohclext.NestedBlock_TUPLE:
				// We won't know the actual tuple type until we have a real
				// value to choose it from.
				atys[elem.TypeName] = cty.DynamicPseudoType

			case protohclext.NestedBlock_LIST:
				if nestedTy.HasDynamicTypes() {
					return schemaErrorf(field.FullName(), "can't use (hcl.block).kind = LIST with a block type containing an attribute with an 'any' constraint")
				}
				atys[elem.TypeName] = cty.List(nestedTy)

			case protohclext.NestedBlock_SET:
				if nestedTy.HasDynamicTypes() {
					return schemaErrorf(field.FullName(), "can't use (hcl.block).kind = SET with a block type containing an attribute with an 'any' constraint")
				}
				atys[elem.TypeName] = cty.Set(nestedTy)

			default:
				return schemaErrorf(field.FullName(), "unsupported block collection kind %s", elem.CollectionKind)
			}

		case FieldFlattened:
			// For flattened we'll keep writing into the same map, but we'll
			// use the nested message descriptor as the source instead.
			nestedDesc := elem.Nested
			err := buildObjectTypeAtysForMessageDesc(nestedDesc, atys)
			if err != nil {
				return err
			}

		case FieldBlockLabel:
			// A block label should always be a singleton string, or else the
			// schema is invalid.
			if field.Kind() != protoreflect.StringKind || field.IsList() || field.IsMap() {
				return schemaErrorf(field.FullName(), "only string fields can be used for block labels")
			}
			atys[elem.Name] = cty.String

		default:
			panic(fmt.Sprintf("unhandled field element type %T", elem))
		}
	}

	return nil
}
