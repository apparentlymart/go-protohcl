package protohcl

import (
	"fmt"

	hcl "github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty/convert"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// DecodeBody decodes the content of the given body into a message that
// conforms to the given message descriptor.
//
// You can use this method directly only if the caller has access to generated
// stub code for the relevant protobuf schema. If you need to work with
// schemas loaded only at runtime, such as over a plugin wire protocol, use
// DynamicProto instead.
func DecodeBody(body hcl.Body, desc protoreflect.MessageDescriptor, ctx *hcl.EvalContext) (proto.Message, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	schema, err := bodySchema(desc)
	if err != nil {
		// If the schema isn't valid at all then this is really a bug in
		// whatever software defined the schema, but we'll just bundle it
		// up as a diagnostic here so that callers don't need to deal with
		// two different ways to handle errors.
		diags = diags.Append(schemaErrorDiagnostic(err))
	}

	content, moreDiags := body.Content(schema)
	diags = append(diags, moreDiags...)
	// Even if there were errors, we'll try a partial decode anyway.

	msg := newMessageMaybeDynamic(desc)
	moreDiags = fillMessageFromContent(content, body.MissingItemRange(), msg, ctx, diags.HasErrors())
	diags = append(diags, moreDiags...)

	return msg.Interface(), diags
}

func fillMessageFromContent(content *hcl.BodyContent, missingRange hcl.Range, msg protoreflect.Message, ctx *hcl.EvalContext, recovering bool) hcl.Diagnostics {
	var diags hcl.Diagnostics

	// Our task here is to walk the message descriptor graph associated with
	// "msg" and try to find a corresponding item in "content" to populate
	// each annotated field from.

	fields := msg.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		elem, err := GetFieldElem(field)
		if err != nil {
			diags = diags.Append(schemaErrorDiagnostic(err))
		}

		switch elem := elem.(type) {
		case FieldAttribute:
			// We'll always at least _clear_ the field, but we might then
			// populate it with a new value below, if we can find a suitable
			// value.
			msg.Clear(field)

			attr, exists := content.Attributes[elem.Name]
			if !exists {
				if elem.Required {
					// We shouldn't get here because the body should already
					// have enforced "Required" during decoding, but we'll
					// handle it here anyway to be robust.
					diags = append(diags, &hcl.Diagnostic{
						Severity: hcl.DiagError,
						Summary:  "Missing required argument",
						Detail:   fmt.Sprintf("The argument %q is required, but no definition was found.", elem.Name),
						Subject:  missingRange.Ptr(),
					})
				}
				continue
			}

			val, moreDiags := attr.Expr.Value(ctx)
			diags = append(diags, moreDiags...)
			if moreDiags.HasErrors() {
				continue
			}

			wantTy, moreDiags := elem.TypeConstraint()
			diags = append(diags, moreDiags...)
			if moreDiags.HasErrors() {
				continue
			}

			// We have two stages of conversion: the first deals with the
			// HCL-specific type constraint that might've been set using the
			// (hcl.attr).type option, but then we also impose any constraints
			// implied by the protobuf field's own type. Specifying these
			// separately allows for some special situations, such as declaring
			// (hcl.attr).type = "number" for a protobuf string field, which
			// allows capturing a decimal representation of the full precision
			// of the given number, rather than limiting it to one of the
			// protobuf number types.
			val, err = convert.Convert(val, wantTy)
			if err != nil {
				diags = append(diags, &hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  unsuitableValueSummary,
					Detail: fmt.Sprintf(
						"Inappropriate value for attribute %q: %s.",
						elem.Name, err.Error(),
					),
					Subject:     attr.Expr.Range().Ptr(),
					Context:     hcl.RangeBetween(attr.NameRange, attr.Expr.Range()).Ptr(),
					Expression:  attr.Expr,
					EvalContext: ctx,
				})
				continue
			}

			if val.IsNull() {
				if elem.Required {
					// We can get here if the attribute was defined but ended
					// up having a null value. We treat that the same as having
					// omitted it entirely, but the HCL low-level API doesn't
					// do that automatically.
					diags = append(diags, &hcl.Diagnostic{
						Severity: hcl.DiagError,
						Summary:  unsuitableValueSummary,
						Detail: fmt.Sprintf(
							"Attribute %q is required, so must not be null.",
							elem.Name,
						),
						Subject:     attr.Expr.Range().Ptr(),
						Context:     hcl.RangeBetween(attr.NameRange, attr.Expr.Range()).Ptr(),
						Expression:  attr.Expr,
						EvalContext: ctx,
					})
				}
				// We'll just leave the field cleared, then.
				continue
			}

			needTy, err := valuePhysicalConstraintForFieldKind(val.Type(), field)
			if err != nil {
				diags = diags.Append(schemaErrorDiagnostic(err))
			}
			val, err = convert.Convert(val, needTy)
			if err != nil {
				diags = append(diags, &hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  unsuitableValueSummary,
					Detail: fmt.Sprintf(
						"Inappropriate value for attribute %q: %s.",
						elem.Name, err.Error(),
					),
					Subject:     attr.Expr.Range().Ptr(),
					Context:     hcl.RangeBetween(attr.NameRange, attr.Expr.Range()).Ptr(),
					Expression:  attr.Expr,
					EvalContext: ctx,
				})
				continue
			}

			protoVal, moreDiags := protoValueForField(val, attr.Expr.Range(), msg, field)
			diags = append(diags, moreDiags...)
			if moreDiags.HasErrors() {
				continue
			}

			msg.Set(field, protoVal)
		case FieldNestedBlockType:
			// We'll always at least _clear_ the field, but we might then
			// populate it with a new value below, if we can find a suitable
			// value.
			msg.Clear(field)

			if elem.Repeated {
				// For a repeated block type we'll write in all of the blocks
				// of the associated type.
				list := msg.NewField(field).List()
				for _, block := range content.Blocks {
					if block.Type != elem.TypeName {
						continue
					}
					nestedMsg, moreDiags := newMessageForBlock(block, elem, ctx)
					diags = append(diags, moreDiags...)
					list.Append(protoreflect.ValueOfMessage(nestedMsg))
				}
			} else {
				// For a singleton block there should be at most one block
				// of the associated type.
				var found *hcl.Block
				for _, block := range content.Blocks {
					if block.Type != elem.TypeName {
						continue
					}
					if found != nil {
						diags = append(diags, &hcl.Diagnostic{
							Severity: hcl.DiagError,
							Summary:  fmt.Sprintf("Duplicate %s block", elem.TypeName),
							Detail: fmt.Sprintf(
								"There may be no more than one %s block. Previous block declared at %s.",
								elem.TypeName, found.DefRange.Ptr(),
							),
							Subject: block.TypeRange.Ptr(),
							Context: block.DefRange.Ptr(),
						})
						break
					}
					found = block
					nestedMsg, moreDiags := newMessageForBlock(block, elem, ctx)
					diags = append(diags, moreDiags...)
					msg.Set(field, protoreflect.ValueOfMessage(nestedMsg))
				}
			}

		case FieldFlattened:
			// For a "flattened" message we keep working with the same
			// hcl.BodyContent but we must start a new message with the
			// child descriptor.
			msg.Clear(field)
			nestedMsg := newMessageMaybeDynamic(elem.Nested)
			moreDiags := fillMessageFromContent(content, missingRange, nestedMsg, ctx, recovering)
			diags = append(diags, moreDiags...)
			msg.Set(field, protoreflect.ValueOfMessage(nestedMsg))
		}
	}

	return diags
}

func newMessageForBlock(block *hcl.Block, elem FieldNestedBlockType, ctx *hcl.EvalContext) (protoreflect.Message, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	nestedMsg, moreDiags := DecodeBody(block.Body, elem.Nested, ctx)
	diags = append(diags, moreDiags...)
	nestedMsgR := nestedMsg.ProtoReflect()

	nestedFields := elem.Nested.Fields()
	nextLabel := 0
	for i := 0; i < nestedFields.Len(); i++ {
		nestedField := nestedFields.Get(i)
		elem, err := GetFieldElem(nestedField)
		if err != nil {
			continue // we handle these errors during schema construction
		}
		if _, ok := elem.(FieldBlockLabel); ok {
			nestedMsgR.Set(nestedField, protoreflect.ValueOfString(block.Labels[nextLabel]))
			nextLabel++
		}
	}

	return nestedMsgR, diags
}
