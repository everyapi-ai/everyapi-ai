package tools

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestWriteCodexModelCatalogUltrafast(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bundled string
		models  []Model
		want    [][]string
	}{
		{
			name: "bundled catalog predates API availability",
			bundled: `{"models":[
				{"slug":"gpt-6.1-sol","service_tiers":[{"id":"priority","name":"Fast"}],"additional_speed_tiers":["fast"]},
				{"slug":"gpt-6-astra","service_tiers":[{"id":"priority","name":"Fast"}]},
				{"slug":"gpt-6-sol","service_tiers":[{"id":"priority","name":"Fast"}]}
			]}`,
			models: []Model{{ID: "gpt-6.1-sol"}, {ID: "gpt-6-astra"}, {ID: "gpt-6-sol"}, {ID: "claude-opus-5"}, {ID: "gpt-6.1-sol-custom"}},
			want:   [][]string{{"priority", "ultrafast"}, {"priority", "ultrafast"}, {"priority"}, {}, {}},
		},
		{
			name:    "supported models missing from older bundled catalog",
			bundled: `{"models":[{"slug":"gpt-template","service_tiers":[{"id":"priority","name":"Fast"}]}]}`,
			models:  []Model{{ID: "gpt-6.1-sol"}, {ID: "gpt-6-astra"}, {ID: "unknown-model"}},
			want:    [][]string{{"ultrafast"}, {"ultrafast"}, {}},
		},
		{
			name:    "preserve newer bundled metadata without duplicating tiers",
			bundled: `{"models":[{"slug":"gpt-6.1-sol","service_tiers":[{"id":"ultrafast","name":"Native Ultrafast","description":"Native description","future_field":true},{"id":"priority","name":"Fast"}]}]}`,
			models:  []Model{{ID: "gpt-6.1-sol"}},
			want:    [][]string{{"ultrafast", "priority"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := codexBundledCatalog
			codexBundledCatalog = func() ([]byte, error) { return []byte(tc.bundled), nil }
			t.Cleanup(func() { codexBundledCatalog = original })
			path, err := writeCodexModelCatalog(t.TempDir(), tc.models)
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Models []struct {
					Slug         string           `json:"slug"`
					ServiceTiers []map[string]any `json:"service_tiers"`
				} `json:"models"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Models) != len(tc.models) {
				t.Fatalf("catalog has %d models, want %d", len(got.Models), len(tc.models))
			}
			for i, model := range got.Models {
				if model.Slug != tc.models[i].ID {
					t.Fatalf("model %d = %q, want %q", i, model.Slug, tc.models[i].ID)
				}
				ids := []string{}
				for _, tier := range model.ServiceTiers {
					id, _ := tier["id"].(string)
					ids = append(ids, id)
					if id == "ultrafast" && (tier["name"] == "" || tier["description"] == "") {
						t.Errorf("%s: Ultrafast tier lacks display metadata: %v", model.Slug, tier)
					}
				}
				if !reflect.DeepEqual(ids, tc.want[i]) {
					t.Errorf("%s: service tiers = %v, want %v", model.Slug, ids, tc.want[i])
				}
			}
			if tc.name == "preserve newer bundled metadata without duplicating tiers" {
				tier := got.Models[0].ServiceTiers[0]
				if tier["name"] != "Native Ultrafast" || tier["description"] != "Native description" || tier["future_field"] != true {
					t.Errorf("native Ultrafast metadata changed: %v", tier)
				}
			}
		})
	}
}
