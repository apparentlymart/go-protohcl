package protohcl

import (
	"fmt"

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
			v, err := hclValueForProtoFieldValue(msg.Get(field), path, elem, false)
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
			path := append(path, cty.GetAttrStep{Name: elem.TypeName})

			if elem.CollectionKind == protohclext.NestedBlock_AUTO {
				// "AUTO" here really means singleton
				nestedMsg := msg.Get(field).Message()
				nestedObj, err := objectValueForMessage(nestedMsg, path)
				if err != nil {
					return err
				}
				attrs[elem.TypeName] = nestedObj
				continue
			}

			// All of the other kinds call for us to build a slice of
			// elems.
			var elems []cty.Value
			msgList := msg.Get(field).List()
			if listLen := msgList.Len(); listLen > 0 {
				elems = make([]cty.Value, listLen)
				for i := range elems {
					nestedMsg := msgList.Get(i).Message()
					nestedObj, err := objectValueForMessage(nestedMsg, path)
					if err != nil {
						return err
					}
					elems[i] = nestedObj
				}
			}

			switch elem.CollectionKind {
			case protohclext.NestedBlock_TUPLE:
				attrs[elem.TypeName] = cty.TupleVal(elems)
			case protohclext.NestedBlock_LIST:
				if len(elems) == 0 {
					nestedTy, err := ObjectTypeConstraintForMessageDesc(field.Message())
					if err != nil {
						return err
					}
					attrs[elem.TypeName] = cty.ListValEmpty(nestedTy)
				} else {
					attrs[elem.TypeName] = cty.ListVal(elems)
				}
			case protohclext.NestedBlock_SET:
				if len(elems) == 0 {
					nestedTy, err := ObjectTypeConstraintForMessageDesc(field.Message())
					if err != nil {
						return err
					}
					attrs[elem.TypeName] = cty.SetValEmpty(nestedTy)
				} else {
					attrs[elem.TypeName] = cty.SetVal(elems)
				}
			default:
				return schemaErrorf(field.FullName(), "unsupported collection kind %s", elem.CollectionKind)
			}

		case FieldFlattened:
			// For flattened we'll keep writing into the same map, but we'll
			// use the nested message as the source instead.
			nestedMsg := msg.Get(field).Message()
			err := buildObjectValueAttrsForMessage(nestedMsg, path, attrs)
			if err != nil {
				return err
			}

		case FieldBlockLabel:
			// A block label should always be a singleton string, or else the
			// schema is invalid.
			labelVal, ok := msg.Get(field).Interface().(string)
			if !ok {
				return schemaErrorf(field.FullName(), "only string fields can be used for block labels")
			}
			attrs[elem.Name] = cty.StringVal(labelVal)

		default:
			panic(fmt.Sprintf("unhandled field element type %T", elem))
		}
	}

	// If we get here without early-returning an error then we succeeded.
	return nil
}

func hclValueForProtoFieldValue(val protoreflect.Value, path cty.Path, attr FieldAttribute, subElem bool) (cty.Value, error) {
	// Here we're really using the subset of normal Go types that
	// protoreflect.Value uses internally, which is good enough for our goals,
	// since the caller will convert the result into the exact type that
	// the field is supposed to produce, anyway.

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
		if subElem {
			// We can only decode a "bytes" value that's directly in an
			// annotated field. It's not valid to have a list or map of raw,
			// and thus we reject this.
			return cty.NilVal, schemaErrorf(attr.TargetField.FullName(), "can only use bytes directly as a raw field, not as element of collection in another field")
		}
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
		// We always return a tuple at this layer, but the caller might then
		// convert it into a list or set if the field options call for it.
		if raw.Len() == 0 {
			return cty.EmptyTupleVal, nil
		}
		elems := make([]cty.Value, raw.Len())
		for i := range elems {
			path := append(path, cty.IndexStep{Key: cty.NumberIntVal(int64(i))})
			elemVal, err := hclValueForProtoFieldValue(raw.Get(i), path, attr, true)
			if err != nil {
				return cty.NilVal, err
			}
			elems[i] = elemVal
		}
		return cty.TupleVal(elems), nil
	case protoreflect.Map:
		// We always return an object at this layer, but the caller might then
		// convert it into a list or set if the field options call for it.
		if raw.Len() == 0 {
			return cty.EmptyObjectVal, nil
		}
		attrs := make(map[string]cty.Value, raw.Len())
		var err error
		raw.Range(func(protoK protoreflect.MapKey, protoV protoreflect.Value) bool {
			k, ok := protoK.Interface().(string)
			if !ok {
				// Should typically have caught this earlier, but we might
				// catch it only here if we're directly encoding a value that
				// isn't directly part of schema.
				err = schemaErrorf(attr.TargetField.FullName(), "HCL doesn't support non-string map keys")
				return false
			}

			path := append(path, cty.IndexStep{Key: cty.StringVal(k)})
			attrs[k], err = hclValueForProtoFieldValue(protoV, path, attr, true)
			if err != nil {
				return false
			}
			return true
		})
		if err != nil {
			return cty.NilVal, err
		}
		return cty.ObjectVal(attrs), nil
	default:
		// We shouldn't get here because the above should be exhaustive for
		// all value types that our decoder can handle.
		return cty.NilVal, schemaErrorf(attr.TargetField.FullName(), "can't convert %T to HCL value", raw)
	}
}
