package protohcl

import (
	"testing"

	"github.com/apparentlymart/go-protohcl/protohcl/internal/testschema"
	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/hcl/v2"
)

func TestBodySchema(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		rootSchema := testschema.File_testschema_proto.Messages().ByName("Root")
		got, err := bodySchema(rootSchema)
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}

		want := &hcl.BodySchema{
			Attributes: []hcl.AttributeSchema{
				{Name: "name"},
			},
			Blocks: []hcl.BlockHeaderSchema{
				{
					Type:       "thing",
					LabelNames: []string{"name"},
				},
			},
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("wrong schema\n%s", diff)
		}
	})
}
