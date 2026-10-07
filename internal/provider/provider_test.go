package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestProviderSchemaIsValid(t *testing.T) {
	resp := &provider.SchemaResponse{}
	New("test")().Schema(context.Background(), provider.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("invalid schema: %v", diags)
	}
}

func TestResolveConfig(t *testing.T) {
	env := map[string]string{
		"JUMPCLOUD_API_KEY": "env-key",
		"JUMPCLOUD_ORG_ID":  "env-org",
		"JUMPCLOUD_API_URL": "https://console.eu.jumpcloud.com/",
	}
	getenv := func(k string) string { return env[k] }
	noenv := func(string) string { return "" }

	tests := []struct {
		name    string
		data    providerModel
		getenv  func(string) string
		want    Config
		wantErr string
	}{
		{
			name:   "defaults api_url",
			data:   providerModel{APIKey: types.StringValue("k")},
			getenv: noenv,
			want:   Config{APIKey: "k", APIURL: defaultAPIURL},
		},
		{
			name:   "falls back to environment and trims trailing slash",
			data:   providerModel{},
			getenv: getenv,
			want:   Config{APIKey: "env-key", OrgID: "env-org", APIURL: "https://console.eu.jumpcloud.com"},
		},
		{
			name: "arguments override environment",
			data: providerModel{
				APIKey: types.StringValue("arg-key"),
				OrgID:  types.StringValue("arg-org"),
				APIURL: types.StringValue("http://localhost:8080"),
			},
			getenv: getenv,
			want:   Config{APIKey: "arg-key", OrgID: "arg-org", APIURL: "http://localhost:8080"},
		},
		{
			name:    "missing api key",
			data:    providerModel{},
			getenv:  noenv,
			wantErr: "missing API key",
		},
		{
			name:    "relative api_url",
			data:    providerModel{APIKey: types.StringValue("k"), APIURL: types.StringValue("console.jumpcloud.com")},
			getenv:  noenv,
			wantErr: "invalid api_url",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveConfig(tt.data, tt.getenv)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *got != tt.want {
				t.Fatalf("got %+v, want %+v", *got, tt.want)
			}
		})
	}
}

func TestConfigure(t *testing.T) {
	t.Setenv("JUMPCLOUD_API_KEY", "")
	t.Setenv("JUMPCLOUD_ORG_ID", "")
	t.Setenv("JUMPCLOUD_API_URL", "")

	ctx := context.Background()
	p := New("test")()
	schemaResp := &provider.SchemaResponse{}
	p.Schema(ctx, provider.SchemaRequest{}, schemaResp)
	objType := schemaResp.Schema.Type().TerraformType(ctx)

	configure := func(apiKey tftypes.Value) *provider.ConfigureResponse {
		raw := tftypes.NewValue(objType, map[string]tftypes.Value{
			"api_key": apiKey,
			"org_id":  tftypes.NewValue(tftypes.String, nil),
			"api_url": tftypes.NewValue(tftypes.String, nil),
		})
		resp := &provider.ConfigureResponse{}
		p.Configure(ctx, provider.ConfigureRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: raw}}, resp)
		return resp
	}

	t.Run("known values", func(t *testing.T) {
		resp := configure(tftypes.NewValue(tftypes.String, "k"))
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		cfg, ok := resp.ResourceData.(*Config)
		if !ok || cfg.APIKey != "k" || cfg.APIURL != defaultAPIURL {
			t.Fatalf("resource data = %#v", resp.ResourceData)
		}
		if resp.DataSourceData != resp.ResourceData {
			t.Fatal("data sources and resources should share the same config")
		}
	})

	t.Run("unknown api key", func(t *testing.T) {
		resp := configure(tftypes.NewValue(tftypes.String, tftypes.UnknownValue))
		if !resp.Diagnostics.HasError() || resp.ResourceData != nil {
			t.Fatalf("expected an error and no provider data, got %v", resp.Diagnostics)
		}
	})

	t.Run("missing api key", func(t *testing.T) {
		resp := configure(tftypes.NewValue(tftypes.String, nil))
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected an error for a missing API key")
		}
	})
}
