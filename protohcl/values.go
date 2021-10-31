package protohcl

import (
	"fmt"
	"math"
	"math/big"

	"github.com/apparentlymart/go-protohcl/protohcl/protohclext"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"
	ctymsgpack "github.com/zclconf/go-cty/cty/msgpack"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const unsuitableValueSummary = "Unsuitable attribute value"

func protoValueForField(val cty.Value, rng hcl.Range, msg protoreflect.Message, field protoreflect.FieldDescriptor) (protoreflect.Value, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	ty := val.Type()

	switch {
	case field.IsList():
		if ty.IsListType() || ty.IsSetType() || ty.IsTupleType() {
			return protoValueForListField(val.AsValueSlice(), rng, msg, field)
		} else {
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  unsuitableValueSummary,
				Detail:   "This argument requires a sequence of values.",
				Subject:  &rng,
			})
		}
	case field.IsMap():
		if ty.IsMapType() || ty.IsObjectType() {
			return protoValueForMapField(val.AsValueMap(), rng, msg, field)
		} else {
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  unsuitableValueSummary,
				Detail:   "This argument requires a mapping from strings to values.",
				Subject:  &rng,
			})
		}
	default:
		return protoValueForSingletonField(val, rng, msg, field)
	}

	return msg.NewField(field), diags
}

func protoValueForSingletonField(val cty.Value, rng hcl.Range, msg protoreflect.Message, field protoreflect.FieldDescriptor) (protoreflect.Value, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	// NOTE: Our logic here assumes that val was already constrained by
	// conversion to the result of valuePhysicalConstraintForFieldKind,
	// and so we only check here for constraints that are tighter than that
	// function's result can represent.

	elem, err := GetFieldElem(field)
	if err != nil {
		diags = diags.Append(schemaErrorDiagnostic(err))
		return protoreflect.ValueOf(nil), diags
	}
	attr, ok := elem.(FieldAttribute)
	if !ok {
		// We should never get here if we're not targeting an attribute.
		panic(fmt.Sprintf("decoding value into %T, not FieldAttribute", elem))
	}

	if attr.RawMode != protohclext.Attribute_NOT_RAW {
		if got, want := field.Kind(), protoreflect.BytesKind; got != want {
			// Should've caught this mismatch while building the HCL schema
			panic(fmt.Sprintf("raw-decoding into %s, not %s", got, want))
		}
		return protoValueForSingletonRawField(val, rng, attr)
	} else if field.Kind() == protoreflect.BytesKind {
		// Should've caught this mismatch while building the HCL schema
		panic(fmt.Sprintf("bytes field %s doesn't have raw mode enabled", field.FullName()))
	}

	if !val.IsKnown() {
		// Only raw-mode fields can accept unknown values.
		// This is not a very actionable error message, so applications that
		// deal with unknown values but know they will be decoding into non-raw
		// fields should ideally catch this problem themselves before trying
		// to decode with protohcl.
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  unsuitableValueSummary,
			Detail:   "Unknown values are not allowed here.",
			Context:  rng.Ptr(),
		})
		return msg.NewField(field), diags
	}

	// By the time we get here, we know that the top-level value is known
	// (because we checked that above) and non-null (because callers should
	// check that before they call, and just skip setting the field if so.)
	ret, moreDiags := protoValueForSingletonFieldKind(val, rng, msg, field)
	diags = append(diags, moreDiags...)
	return ret, diags
}

func protoValueForSingletonFieldKind(val cty.Value, rng hcl.Range, msg protoreflect.Message, field protoreflect.FieldDescriptor) (protoreflect.Value, hcl.Diagnostics) {
	// This function makes its selections based only on the field's kind and
	// not on its HCL-specific options. By the time we get here the caller
	// should already have rejected any null or unknown values and know it's
	// not supposed to be decoding in raw mode.

	var diags hcl.Diagnostics

	switch field.Kind() {
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(val.True()), diags
	case protoreflect.EnumKind:
		// TODO: Need some more work here to allow annotating proto enum
		// values with the strings that will select them in config.
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  unsuitableValueSummary,
			Detail:   "Decoding enum-typed fields isn't supported yet.",
			Context:  rng.Ptr(),
		})
		return msg.NewField(field), diags
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		bi, moreDiags := intValueForFixedIntegerField(val, rng, math.MinInt32, math.MaxInt32)
		diags = append(diags, moreDiags...)
		return protoreflect.ValueOfInt32(int32(bi.Int64())), diags
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		bi, moreDiags := intValueForFixedIntegerField(val, rng, math.MinInt64, math.MaxInt64)
		diags = append(diags, moreDiags...)
		return protoreflect.ValueOfInt64(bi.Int64()), diags
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		bi, moreDiags := intValueForFixedIntegerField(val, rng, 0, math.MaxUint32)
		diags = append(diags, moreDiags...)
		return protoreflect.ValueOfUint32(uint32(bi.Uint64())), diags
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		bi, moreDiags := intValueForFixedIntegerField(val, rng, 0, math.MaxUint64)
		diags = append(diags, moreDiags...)
		return protoreflect.ValueOfUint64(bi.Uint64()), diags
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(val.AsString()), diags
	case protoreflect.MessageKind:
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  unsuitableValueSummary,
			Detail:   "Decoding message-typed fields isn't supported yet.",
			Context:  rng.Ptr(),
		})
		return msg.NewField(field), diags
	default:
		// physicalConstraintForFieldKindSingle rejects all other kinds,
		// so if we get here then it's always a bug.
		panic(fmt.Sprintf("unhandled %s for field %s", field.Kind(), field.FullName()))
	}

}

func protoValueForSingletonRawField(val cty.Value, rng hcl.Range, attr FieldAttribute) (protoreflect.Value, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	ty, moreDiags := attr.TypeConstraint()
	if diags.HasErrors() {
		diags = append(diags, moreDiags...)
		return protoreflect.ValueOfBytes(nil), diags
	}

	var rawVal []byte
	var err error
	switch attr.RawMode {
	case protohclext.Attribute_MESSAGEPACK:
		rawVal, err = ctymsgpack.Marshal(val, ty)
		if err != nil {
			// This is a weird situation because we're reporting what must be
			// a bug in the calling program, but with a message directed at
			// the configuration author.
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Internal error while decoding configuration",
				Detail:   fmt.Sprintf("This attribute value is not compatible with the MessagePack field where it'll be stored internally: %s.\n\nThis is a bug in the configuration schema.", err),
			})
			return protoreflect.ValueOfBytes(nil), diags
		}
	case protohclext.Attribute_JSON:
		rawVal, err = ctyjson.Marshal(val, ty)
		if err != nil {
			// This is a weird situation because we're reporting what must be
			// a bug in the calling program, but with a message directed at
			// the configuration author.
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Internal error while decoding configuration",
				Detail:   fmt.Sprintf("This attribute value is not compatible with the JSON field where it'll be stored internally: %s.\n\nThis is a bug in the configuration schema.", err),
			})
			return protoreflect.ValueOfBytes(nil), diags
		}

	case protohclext.Attribute_NOT_RAW:
		// Caller shouldn't call this function if not in raw mode.
		panic("attempting raw encoding into a non-raw field")

	default:
		diags = diags.Append(schemaErrorDiagnostic(
			schemaErrorf(attr.TargetField.FullName(), "invalid raw mode %s", attr.RawMode),
		))
		return protoreflect.ValueOfBytes(nil), diags
	}

	return protoreflect.ValueOfBytes(rawVal), diags
}

// intValueForFixedIntegerField checks that the value is an integer within
// the given range, and if so returns it as a *big.Int so that the caller
// can then convert it from there to a suitable fixed-size integer type.
//
// This function always returns a non-nil *big.Int, but if it also returns
// error diagnostics then that integer might not be in range.
func intValueForFixedIntegerField(val cty.Value, rng hcl.Range, min int64, max uint64) (*big.Int, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	bf := val.AsBigFloat()
	bi, _ := bf.Int(nil)
	if !bf.IsInt() {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  unsuitableValueSummary,
			Detail:   fmt.Sprintf("The value must be a whole number."),
			Subject:  rng.Ptr(),
		})
		return bi, diags
	}

	bigMin := big.NewInt(min)
	if cmpMin := bi.Cmp(bigMin); cmpMin < 0 {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  unsuitableValueSummary,
			Detail:   fmt.Sprintf("The value must be greater than or equal to %d.", min),
			Subject:  rng.Ptr(),
		})
		return bi, diags
	}
	bigMax := big.NewInt(0)
	bigMax.SetUint64(max)
	if cmpMax := bi.Cmp(bigMax); cmpMax > 0 {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  unsuitableValueSummary,
			Detail:   fmt.Sprintf("The value must be less than or equal to %d.", max),
			Subject:  rng.Ptr(),
		})
		return bi, diags
	}

	return bi, diags
}

func protoValueForListField(vals []cty.Value, rng hcl.Range, msg protoreflect.Message, field protoreflect.FieldDescriptor) (protoreflect.Value, hcl.Diagnostics) {
	var diags hcl.Diagnostics
	list := msg.NewField(field).List()

	for _, v := range vals {
		protoVal, moreDiags := protoValueForSingletonField(v, rng, msg, field)
		diags = append(diags, moreDiags...)
		if moreDiags.HasErrors() {
			continue
		}
		list.Append(protoVal)
	}

	return protoreflect.ValueOfList(list), diags
}

func protoValueForMapField(vals map[string]cty.Value, rng hcl.Range, msg protoreflect.Message, field protoreflect.FieldDescriptor) (protoreflect.Value, hcl.Diagnostics) {
	var diags hcl.Diagnostics
	protoMap := msg.NewField(field).Map()

	for k, v := range vals {
		if !v.IsKnown() {
			// Only raw-mode fields can accept unknown values, and we don't
			// allow maps of raw so we can't get here in that case.
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  unsuitableValueSummary,
				Detail:   "Unknown values are not allowed here.",
				Context:  rng.Ptr(), // NOTE: Non-ideal because we're reporting the overall map range, not the individual element
			})
			return msg.NewField(field), diags
		}

		// In protobuf a map is really just a repeated message of a special
		// generated message type with key and value fields, so the values
		// we're constructing here are for the value field of that hidden
		// message type, not directly for what "field" is describing.
		mapValField := field.MapValue()
		mapElemMsg := newMessageMaybeDynamic(mapValField.ContainingMessage())
		protoVal, moreDiags := protoValueForSingletonFieldKind(v, rng, mapElemMsg, mapValField)
		diags = append(diags, moreDiags...)
		if moreDiags.HasErrors() {
			continue
		}
		protoMap.Set(protoreflect.ValueOfString(k).MapKey(), protoVal)
	}

	return protoreflect.ValueOfMap(protoMap), diags
}

// valuePhysicalConstraintForFieldKind produces a cty type constraint that
// approximates the physical storage constraints of the target field, based
// on the value that will be constrained. This is mainly just to reduce the
// number of cases that our value-to-field decoder needs to deal with, by
// doing some basic type normalization up front.
func valuePhysicalConstraintForFieldKind(ty cty.Type, field protoreflect.FieldDescriptor) (cty.Type, error) {
	switch {
	case field.IsList():
		ety, err := physicalConstraintForFieldKindSingle(field)
		if err != nil {
			return cty.DynamicPseudoType, err
		}
		switch {
		case ty.IsTupleType():
			// Our per-element type constraint may not be an exact type and so
			// the final result might end up having a different value for each
			// element, and so we'll always construct a tuple type even though
			// we're going to specify the same type _constraint_ for each
			// of its elements.
			etys := make([]cty.Type, len(ty.TupleElementTypes()))
			for i := range etys {
				etys[i] = ety
			}
			return cty.Tuple(etys), nil
		case ty.IsListType() || ty.IsSetType():
			// If we're already holding a list or set then we know the final
			// result must always have homogenous element types because the
			// input already has homogenous element types. However, we'll
			// normalize down to always using a list because a protobuf list
			// field can't preserve our special set behaviors anyway.
			return cty.List(ety), nil
		}
		return cty.DynamicPseudoType, schemaErrorf(field.FullName(), "can't populate list field from HCL %s", ty.FriendlyName())

	case field.IsMap():
		ety, err := physicalConstraintForFieldKindSingle(field.MapValue())
		if err != nil {
			return cty.DynamicPseudoType, err
		}
		switch {
		case ty.IsObjectType():
			// Our per-element type constraint may not be an exact type and so
			// the final result might end up having a different value for each
			// element, and so we'll always construct an object type even though
			// we're going to specify the same type _constraint_ for each
			// of its elements.
			atys := make(map[string]cty.Type)
			for name := range ty.AttributeTypes() {
				atys[name] = ety
			}
			return cty.Object(atys), nil
		case ty.IsMapType():
			// If we're already holding a map then we know the final result
			// must always have homogenous element types, but we might still
			// convert those elements to a different homogenous type.
			return cty.Map(ety), nil
		}
		return cty.DynamicPseudoType, schemaErrorf(field.FullName(), "can't populate map field from HCL %s", ty.FriendlyName())

	default:
		return physicalConstraintForFieldKindSingle(field)
	}
}

func physicalConstraintForFieldKindSingle(field protoreflect.FieldDescriptor) (cty.Type, error) {
	switch field.Kind() {
	case protoreflect.BoolKind:
		return cty.Bool, nil
	case protoreflect.EnumKind:
		return cty.String, nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Uint32Kind, protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Uint64Kind, protoreflect.Sfixed32Kind, protoreflect.Fixed32Kind, protoreflect.Sfixed64Kind, protoreflect.Fixed64Kind, protoreflect.FloatKind, protoreflect.DoubleKind:
		return cty.Number, nil
	case protoreflect.StringKind:
		return cty.String, nil
	case protoreflect.MessageKind:
		// TODO: Support this by inferring an object type constraint from
		// the message type, once we have a "type constraint from message
		// descriptor" helper function.
		return cty.DynamicPseudoType, schemaErrorf(field.FullName(), "cannot decode a HCL value into a message-typed field")
	case protoreflect.BytesKind:
		// We use "bytes" fields for our raw mode, so in that case we want
		// to skip any further constraining of the value so we can just store
		// whatever the HCL result directly.
		return cty.DynamicPseudoType, nil
	default:
		return cty.DynamicPseudoType, schemaErrorf(field.FullName(), "cannot decode a HCL value into a %s field", field.Kind())
	}
}

// autoTypeConstraintForField tries to automatically select a reasonable type
// constraint based on the given field descriptor, for situations where the
// protobuf schema author didn't specify one explicitly.
//
// Returns cty.NilType if there is no suitable corresponding type, in which
// case the schema author _must_ specify one.
func autoTypeConstraintForField(field protoreflect.FieldDescriptor) cty.Type {
	elemField := field
	if field.IsMap() {
		elemField = field.MapValue()
	}

	ety := autoTypeConstraintForFieldElement(elemField)
	if ety == cty.NilType {
		return ety
	}

	switch {
	case field.IsList():
		if ety.HasDynamicTypes() {
			return cty.DynamicPseudoType // will need to choose a tuple type later
		}
		return cty.List(ety)
	case field.IsMap():
		if ety.HasDynamicTypes() {
			return cty.DynamicPseudoType // will need to choose an object type later
		}
		return cty.Map(ety)
	default:
		return ety
	}
}

// autoTypeConstraintForFieldElement is a part of autoTypeConstraintForField
// which ignores the list-ness or map-ness of the field and just returns its
// element type, under the assumption that autoTypeConstraintForField will
// then wrap it in a collection type if needed.
func autoTypeConstraintForFieldElement(field protoreflect.FieldDescriptor) cty.Type {
	switch field.Kind() {
	case protoreflect.BoolKind:
		return cty.Bool
	case protoreflect.EnumKind:
		return cty.String
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Uint32Kind, protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Uint64Kind, protoreflect.Sfixed32Kind, protoreflect.Fixed32Kind, protoreflect.Sfixed64Kind, protoreflect.Fixed64Kind, protoreflect.FloatKind, protoreflect.DoubleKind:
		return cty.Number
	case protoreflect.StringKind:
		return cty.String
	case protoreflect.MessageKind:
		// TODO: Support this by inferring an object type constraint from
		// the message type, once we have a "type constraint from message
		// descriptor" helper function.
		return cty.NilType
	case protoreflect.BytesKind:
		// We use "bytes" fields for our raw mode, which always requires
		// an explicit type constraint.
		return cty.NilType
	default:
		return cty.NilType
	}
}
