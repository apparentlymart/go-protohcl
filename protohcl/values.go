package protohcl

import (
	"github.com/apparentlymart/go-protohcl/protohcl/protohclext"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	ctyjson "github.com/zclconf/go-cty/cty/json"
	ctymsgpack "github.com/zclconf/go-cty/cty/msgpack"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ObjectValueForMessage returns an HCL value, guaranteed to be of an object
// type, which represents the HCL-annotated fields from the given message.
//
// This is intended for situations where a message value will be included in
// a hcl.EvalContext for some later evaluation elsewhere, so that objects
// can refer to one another using HCL's expression language.
//
// Because the HCL-specific field options primarily describe the intended shape
// of configuration input rather than object output, fields representing
// nested blocks will be presented as either object values directly (for
// singletons) or collections of object values (for repeated), based on the
// (hcl.block).kind schema option. There is currently no way to return a
// nested block type as a map using labels as keys.
func ObjectValueForMessage(msg proto.Message) (cty.Value, error) {
	reflectMsg := msg.ProtoReflect()
	path := make(cty.Path, 0, 8) // allow a bit of nesting before we allocate again

	return objectValueForMessage(reflectMsg, path)
}

func objectValueForMessage(msg protoreflect.Message, path cty.Path) (cty.Value, error) {
	attrs := make(map[string]cty.Value)
	err := buildObjectValueAttrsForMessage(msg, path, attrs)
	if err != nil {
		return cty.DynamicVal, err
	}
	return cty.ObjectVal(attrs), nil
}

func buildObjectValueAttrsForMessage(msg protoreflect.Message, path cty.Path, attrs map[string]cty.Value) error {
	fields := msg.Descriptor().Fields()

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
			path := append(path, cty.GetAttrStep{Name: elem.Name})
			v, err := hclValueForProtoFieldValue(msg.Get(field), path, elem)
			if err != nil {
				return err
			}

			// We can lose type information while encoding to protobuf fields,
			// and so we'll now convert back to the declared type constraint.
			ty, diags := elem.TypeConstraint()
			if diags.HasErrors() {
				return schemaErrorf(field.FullName(), "invalid type constraint expression")
			}

			v, err = convert.Convert(v, ty)
			if err != nil {
				return path.NewErrorf("invalid encoding of %s value as %s: %s", ty.FriendlyName(), field.Kind(), err)
			}
			attrs[elem.Name] = v

		case FieldNestedBlockType:
			// TODO: Implement
			return schemaErrorf(field.FullName(), "can't convert nested block types to object fields yet")

		case FieldFlattened:
			// TODO: Implement
			return schemaErrorf(field.FullName(), "can't handle flattened messages in object conversion yet")

		case FieldBlockLabel:
			// TODO: Implement
			return schemaErrorf(field.FullName(), "can't handle block labels in object conversion yet")
		}
	}

	// If we get here without early-returning an error then we succeeded.
	return nil
}

func hclValueForProtoFieldValue(val protoreflect.Value, path cty.Path, attr FieldAttribute) (cty.Value, error) {
	// Here we're really using the subset of normal Go types that
	// protoreflect.Value uses internally, which is good enough for our goals,
	// since the caller will convert the result into the exact type that
	// the field is supposed to produce, anyway.\

	switch raw := val.Interface().(type) {
	case bool:
		return cty.BoolVal(raw), nil
	case int32:
		return cty.NumberIntVal(int64(raw)), nil
	case int64:
		return cty.NumberIntVal(raw), nil
	case uint32:
		return cty.NumberUIntVal(uint64(raw)), nil
	case uint64:
		return cty.NumberUIntVal(raw), nil
	case float32:
		return cty.NumberFloatVal(float64(raw)), nil
	case float64:
		return cty.NumberFloatVal(raw), nil
	case string:
		return cty.StringVal(raw), nil
	case []byte:
		if len(raw) == 0 {
			// A totally-unset raw field is another way to write a null value
			// of its type constraint. We'll just return an untyped null here
			// and let the caller convert it to the appropriate type.
			return cty.NullVal(cty.DynamicPseudoType), nil
		}

		// We use "bytes" fields to represent our raw mode, so our job here
		// is to undo the raw encoding to recover the original value, verbatim.
		ty, diags := attr.TypeConstraint()
		if diags.HasErrors() {
			return cty.NilVal, schemaErrorf(attr.TargetField.FullName(), "invalid type constraint expression")
		}

		var decode func([]byte, cty.Type) (cty.Value, error)
		switch attr.RawMode {
		case protohclext.Attribute_JSON:
			decode = ctyjson.Unmarshal
		case protohclext.Attribute_MESSAGEPACK:
			decode = ctymsgpack.Unmarshal
		default:
			return cty.NilVal, schemaErrorf(attr.TargetField.FullName(), "unsupported raw mode %s", attr.RawMode)
		}

		v, err := decode(raw, ty)
		if err != nil {
			return cty.NilVal, path.NewErrorf("invalid encoding of %s value as bytes: %s", ty.FriendlyName(), err)
		}
		return v, nil

	case protoreflect.EnumNumber:
		// TODO: Handle this once we handle enum types elsewhere too
		return cty.NilVal, schemaErrorf(attr.TargetField.FullName(), "can't convert enum value to HCL value yet")
	case protoreflect.Message:
		// Recursively transform the child message too, then.
		return objectValueForMessage(raw, path)
	case protoreflect.List:
		return cty.NilVal, schemaErrorf(attr.TargetField.FullName(), "can't convert list value to HCL value yet")
	case protoreflect.Map:
		return cty.NilVal, schemaErrorf(attr.TargetField.FullName(), "can't convert map value to HCL value yet")
	default:
		// We shouldn't get here because the above should be exhaustive for
		// all value types that our decoder can handle.
		return cty.NilVal, schemaErrorf(attr.TargetField.FullName(), "can't convert %T to HCL value", raw)
	}
}
