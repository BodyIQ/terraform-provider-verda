// Copyright 2026 Verda Cloud Oy
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/verda-cloud/verdacloud-sdk-go/pkg/verda"
)

func TestContainerSchemaMatchesScalingAPI(t *testing.T) {
	var response resource.SchemaResponse
	(&ContainerResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if _, ok := response.Schema.Attributes["id"]; !ok {
		t.Fatal("container resource needs a stable ID")
	}
	scaling := response.Schema.Attributes["scaling"].(schema.SingleNestedAttribute)
	if _, ok := scaling.Attributes["deadline_seconds"]; ok {
		t.Fatal("Verda does not expose a container request deadline setting")
	}
}

func TestContainerNotFound(t *testing.T) {
	if !isNotFound(&verda.APIError{StatusCode: 404}) {
		t.Fatal("expected API 404 to be recognized")
	}
	if !isNotFound(errors.Join(errors.New("request failed"), &verda.APIError{StatusCode: 404})) {
		t.Fatal("expected wrapped API 404 to be recognized")
	}
	if isNotFound(&verda.APIError{StatusCode: 503}) {
		t.Fatal("503 must not be treated as a deleted deployment")
	}
}

func TestContainerFlattenSetsStableID(t *testing.T) {
	var data ContainerResourceModel
	var diagnostics diag.Diagnostics
	(&ContainerResource{}).flattenDeploymentToModel(context.Background(), &verda.ContainerDeployment{
		Name:      "test-model",
		CreatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}, &data, &diagnostics)
	if diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}
	if data.ID.ValueString() != "test-model" {
		t.Fatalf("expected deployment name as stable ID, got %q", data.ID.ValueString())
	}
}

func TestBuildContainerScalingPatch(t *testing.T) {
	ctx := context.Background()
	down := types.ObjectValueMust(map[string]attr.Type{"delay_seconds": types.Int64Type}, map[string]attr.Value{"delay_seconds": types.Int64Value(30)})
	up := types.ObjectValueMust(map[string]attr.Type{"delay_seconds": types.Int64Type}, map[string]attr.Value{"delay_seconds": types.Int64Value(10)})
	queue := types.ObjectValueMust(map[string]attr.Type{"threshold": types.Float64Type}, map[string]attr.Value{"threshold": types.Float64Value(1)})
	scaling := types.ObjectValueMust(map[string]attr.Type{
		"min_replica_count": types.Int64Type, "max_replica_count": types.Int64Type,
		"queue_message_ttl_seconds": types.Int64Type, "concurrent_requests_per_replica": types.Int64Type,
		"scale_down_policy": down.Type(ctx), "scale_up_policy": up.Type(ctx), "queue_load": queue.Type(ctx),
	}, map[string]attr.Value{
		"min_replica_count": types.Int64Value(0), "max_replica_count": types.Int64Value(2),
		"queue_message_ttl_seconds": types.Int64Value(3600), "concurrent_requests_per_replica": types.Int64Value(1),
		"scale_down_policy": down, "scale_up_policy": up, "queue_load": queue,
	})
	var diagnostics diag.Diagnostics
	patch := buildScalingPatch(ctx, scaling, &diagnostics)
	if diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}
	if patch == nil || *patch.MinReplicaCount != 0 || *patch.MaxReplicaCount != 2 || *patch.QueueMessageTTLSeconds != 3600 {
		t.Fatalf("unexpected scaling patch: %+v", patch)
	}
	if patch.ScalingTriggers.QueueLoad.Threshold != 1 {
		t.Fatalf("unexpected queue trigger: %+v", patch.ScalingTriggers)
	}
}

func TestScalingPatchUsesDedicatedEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth2/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"test","token_type":"Bearer","expires_in":3600,"refresh_token":"test"}`))
			return
		}
		if r.Method != http.MethodPatch || r.URL.Path != "/container-deployments/test-model/scaling" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("invalid JSON: %s", err)
		}
		if payload["min_replica_count"] != float64(0) || payload["queue_message_ttl_seconds"] != float64(3600) {
			t.Errorf("unexpected PATCH payload: %v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"min_replica_count":0,"max_replica_count":2,"queue_message_ttl_seconds":3600,"concurrent_requests_per_replica":1}`))
	}))
	defer server.Close()
	client, err := verda.NewClient(verda.WithBaseURL(server.URL), verda.WithClientID("test"), verda.WithClientSecret("test"), verda.WithAuthBearerToken("test"))
	if err != nil {
		t.Fatal(err)
	}
	min, max, ttl, concurrency := 0, 2, 3600, 1
	_, err = client.ContainerDeployments.UpdateDeploymentScaling(context.Background(), "test-model", &verda.UpdateScalingOptionsRequest{
		MinReplicaCount: &min, MaxReplicaCount: &max, QueueMessageTTLSeconds: &ttl, ConcurrentRequestsPerReplica: &concurrency,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestContainerImageUpdatePreservesUnmanagedEntrypoint(t *testing.T) {
	ctx := context.Background()
	var patched map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/oauth2/token":
			_, _ = w.Write([]byte(`{"access_token":"test","token_type":"Bearer","expires_in":3600,"refresh_token":"test"}`))
		case r.URL.Path == "/container-deployments/test-model/scaling":
			_, _ = w.Write([]byte(`{"min_replica_count":0,"max_replica_count":1,"queue_message_ttl_seconds":3600,"concurrent_requests_per_replica":1,"scale_down_policy":{"delay_seconds":30},"scale_up_policy":{"delay_seconds":0},"scaling_triggers":{"queue_load":{"threshold":1}}}`))
		case r.URL.Path == "/container-deployments/test-model":
			if r.Method == http.MethodPatch {
				if err := json.NewDecoder(r.Body).Decode(&patched); err != nil {
					t.Errorf("invalid patch: %s", err)
				}
			}
			image := "image:old"
			if patched != nil {
				image = "image:new"
			}
			_, _ = w.Write([]byte(`{"name":"test-model","containers":[{"name":"c0","image":{"image":"` + image + `"},"exposed_port":5000,"entrypoint_overrides":{"enabled":true,"entrypoint":["/bin/cog"],"cmd":["serve"]},"env":[],"volume_mounts":[]}],"endpoint_base_url":"https://example.invalid/","created_at":"2026-10-01T00:00:00Z","compute":{"name":"H100","size":1},"container_registry_settings":{"is_private":false},"is_spot":false}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := verda.NewClient(verda.WithBaseURL(server.URL), verda.WithClientID("test"), verda.WithClientSecret("test"))
	if err != nil {
		t.Fatal(err)
	}
	var schemaResp resource.SchemaResponse
	r := &ContainerResource{client: client}
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	s := schemaResp.Schema
	makeState := func(image string) tfsdk.State {
		compute := types.ObjectValueMust(map[string]attr.Type{"name": types.StringType, "size": types.Int64Type}, map[string]attr.Value{"name": types.StringValue("H100"), "size": types.Int64Value(1)})
		down := types.ObjectValueMust(map[string]attr.Type{"delay_seconds": types.Int64Type}, map[string]attr.Value{"delay_seconds": types.Int64Value(30)})
		up := types.ObjectValueMust(map[string]attr.Type{"delay_seconds": types.Int64Type}, map[string]attr.Value{"delay_seconds": types.Int64Value(0)})
		queue := types.ObjectValueMust(map[string]attr.Type{"threshold": types.Float64Type}, map[string]attr.Value{"threshold": types.Float64Value(1)})
		scaling := types.ObjectValueMust(map[string]attr.Type{
			"min_replica_count": types.Int64Type, "max_replica_count": types.Int64Type,
			"queue_message_ttl_seconds": types.Int64Type, "concurrent_requests_per_replica": types.Int64Type,
			"scale_down_policy": down.Type(ctx), "scale_up_policy": up.Type(ctx), "queue_load": queue.Type(ctx),
		}, map[string]attr.Value{
			"min_replica_count": types.Int64Value(0), "max_replica_count": types.Int64Value(1),
			"queue_message_ttl_seconds": types.Int64Value(3600), "concurrent_requests_per_replica": types.Int64Value(1),
			"scale_down_policy": down, "scale_up_policy": up, "queue_load": queue,
		})
		containerType := s.Attributes["containers"].GetType().(types.ListType).ElemType.(types.ObjectType)
		container := types.ObjectValueMust(containerType.AttrTypes, map[string]attr.Value{
			"image": types.StringValue(image), "exposed_port": types.Int64Value(5000),
			"healthcheck":          types.ObjectNull(containerType.AttrTypes["healthcheck"].(types.ObjectType).AttrTypes),
			"entrypoint_overrides": types.ObjectNull(containerType.AttrTypes["entrypoint_overrides"].(types.ObjectType).AttrTypes),
			"env":                  types.ListNull(containerType.AttrTypes["env"].(types.ListType).ElemType),
			"volume_mounts":        types.ListNull(containerType.AttrTypes["volume_mounts"].(types.ListType).ElemType),
		})
		containers := types.ListValueMust(containerType, []attr.Value{container})
		registry := types.ObjectValueMust(map[string]attr.Type{"is_private": types.StringType, "credentials": types.StringType}, map[string]attr.Value{"is_private": types.StringValue("false"), "credentials": types.StringNull()})
		model := ContainerResourceModel{
			ID: types.StringValue("test-model"), Name: types.StringValue("test-model"), IsSpot: types.BoolValue(false),
			Compute: compute, Scaling: scaling, ContainerRegistrySettings: registry, Containers: containers,
			EndpointBaseURL: types.StringValue("https://example.invalid/"), CreatedAt: types.StringValue("2026-10-01T00:00:00Z"),
		}
		state := tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}
		if diags := state.Set(ctx, &model); diags.HasError() {
			t.Fatalf("building state: %v", diags)
		}
		return state
	}
	prior := makeState("image:old")
	plan := makeState("image:new")
	response := resource.UpdateResponse{State: tfsdk.State{Schema: s, Raw: plan.Raw}}
	r.Update(ctx, resource.UpdateRequest{Plan: tfsdk.Plan{Schema: s, Raw: plan.Raw}, State: prior}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", response.Diagnostics)
	}
	if patched == nil {
		t.Fatal("deployment was not patched")
	}
	containersPatch := patched["containers"].([]any)
	updated := containersPatch[0].(map[string]any)
	if updated["name"] != "c0" || updated["image"] != "image:new" {
		t.Errorf("wrong container patch: %v", updated)
	}
	if _, ok := updated["entrypoint_overrides"]; ok {
		t.Errorf("image update must not overwrite Cog entrypoint: %v", updated)
	}
	var id types.String
	response.State.GetAttribute(ctx, path.Root("id"), &id)
	if id.ValueString() != "test-model" {
		t.Errorf("stable ID = %q", id.ValueString())
	}
}
