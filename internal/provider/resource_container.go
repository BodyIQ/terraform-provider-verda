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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/verda-cloud/verdacloud-sdk-go/pkg/verda"
)

var _ resource.Resource = &ContainerResource{}
var _ resource.ResourceWithImportState = &ContainerResource{}

func NewContainerResource() resource.Resource {
	return &ContainerResource{}
}

type ContainerResource struct {
	client *verda.Client
}

type ContainerResourceModel struct {
	ID                        types.String `tfsdk:"id"`
	Name                      types.String `tfsdk:"name"`
	IsSpot                    types.Bool   `tfsdk:"is_spot"`
	Compute                   types.Object `tfsdk:"compute"`
	Scaling                   types.Object `tfsdk:"scaling"`
	ContainerRegistrySettings types.Object `tfsdk:"container_registry_settings"`
	Containers                types.List   `tfsdk:"containers"`
	EndpointBaseURL           types.String `tfsdk:"endpoint_base_url"`
	CreatedAt                 types.String `tfsdk:"created_at"`
}

type ComputeModel struct {
	Name types.String `tfsdk:"name"`
	Size types.Int64  `tfsdk:"size"`
}

type ScalingModel struct {
	MinReplicaCount              types.Int64  `tfsdk:"min_replica_count"`
	MaxReplicaCount              types.Int64  `tfsdk:"max_replica_count"`
	QueueMessageTTLSeconds       types.Int64  `tfsdk:"queue_message_ttl_seconds"`
	ConcurrentRequestsPerReplica types.Int64  `tfsdk:"concurrent_requests_per_replica"`
	ScaleDownPolicy              types.Object `tfsdk:"scale_down_policy"`
	ScaleUpPolicy                types.Object `tfsdk:"scale_up_policy"`
	QueueLoad                    types.Object `tfsdk:"queue_load"`
}

type ScalingPolicyModel struct {
	DelaySeconds types.Int64 `tfsdk:"delay_seconds"`
}

type QueueLoadTriggerModel struct {
	Threshold types.Float64 `tfsdk:"threshold"`
}

type RegistrySettingsModel struct {
	IsPrivate   types.String `tfsdk:"is_private"`
	Credentials types.String `tfsdk:"credentials"`
}

type ContainerModel struct {
	Image               types.String `tfsdk:"image"`
	ExposedPort         types.Int64  `tfsdk:"exposed_port"`
	Healthcheck         types.Object `tfsdk:"healthcheck"`
	EntrypointOverrides types.Object `tfsdk:"entrypoint_overrides"`
	Env                 types.List   `tfsdk:"env"`
	VolumeMounts        types.List   `tfsdk:"volume_mounts"`
}

type HealthcheckModel struct {
	Enabled types.String `tfsdk:"enabled"`
	Port    types.String `tfsdk:"port"`
	Path    types.String `tfsdk:"path"`
}

type EnvVarModel struct {
	Type                     types.String `tfsdk:"type"`
	Name                     types.String `tfsdk:"name"`
	ValueOrReferenceToSecret types.String `tfsdk:"value_or_reference_to_secret"`
}

type EntrypointOverridesModel struct {
	Enabled    types.Bool `tfsdk:"enabled"`
	Entrypoint types.List `tfsdk:"entrypoint"`
	Cmd        types.List `tfsdk:"cmd"`
}

type VolumeMountModel struct {
	Type       types.String `tfsdk:"type"`
	MountPath  types.String `tfsdk:"mount_path"`
	SecretName types.String `tfsdk:"secret_name"`
	SizeInMB   types.Int64  `tfsdk:"size_in_mb"`
	VolumeID   types.String `tfsdk:"volume_id"`
}

func (r *ContainerResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_container"
}

func (r *ContainerResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Verda container deployment for serverless workloads",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Stable deployment ID (the deployment name)",
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Name of the container deployment",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"is_spot": schema.BoolAttribute{
				MarkdownDescription: "Whether to use spot instances (defaults to false)",
				Optional:            true,
				Computed:            true,
			},
			"compute": schema.SingleNestedAttribute{
				MarkdownDescription: "Compute resources for the deployment",
				Required:            true,
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						MarkdownDescription: "GPU type (e.g., 'H100', 'A100')",
						Required:            true,
					},
					"size": schema.Int64Attribute{
						MarkdownDescription: "Number of GPUs",
						Required:            true,
					},
				},
			},
			"scaling": schema.SingleNestedAttribute{
				MarkdownDescription: "Scaling configuration for the deployment",
				Required:            true,
				Attributes: map[string]schema.Attribute{
					"min_replica_count": schema.Int64Attribute{
						MarkdownDescription: "Minimum number of replicas",
						Required:            true,
					},
					"max_replica_count": schema.Int64Attribute{
						MarkdownDescription: "Maximum number of replicas",
						Required:            true,
					},
					"queue_message_ttl_seconds": schema.Int64Attribute{
						MarkdownDescription: "Queue message TTL in seconds",
						Required:            true,
					},
					"concurrent_requests_per_replica": schema.Int64Attribute{
						MarkdownDescription: "Maximum concurrent requests per replica",
						Required:            true,
					},
					"scale_down_policy": schema.SingleNestedAttribute{
						MarkdownDescription: "Scale down policy configuration",
						Required:            true,
						Attributes: map[string]schema.Attribute{
							"delay_seconds": schema.Int64Attribute{
								MarkdownDescription: "Delay in seconds before scaling down",
								Required:            true,
							},
						},
					},
					"scale_up_policy": schema.SingleNestedAttribute{
						MarkdownDescription: "Scale up policy configuration",
						Required:            true,
						Attributes: map[string]schema.Attribute{
							"delay_seconds": schema.Int64Attribute{
								MarkdownDescription: "Delay in seconds before scaling up",
								Required:            true,
							},
						},
					},
					"queue_load": schema.SingleNestedAttribute{
						MarkdownDescription: "Queue load trigger configuration",
						Required:            true,
						Attributes: map[string]schema.Attribute{
							"threshold": schema.Float64Attribute{
								MarkdownDescription: "Queue load threshold for scaling",
								Required:            true,
							},
						},
					},
				},
			},
			"container_registry_settings": schema.SingleNestedAttribute{
				MarkdownDescription: "Container registry authentication settings",
				Optional:            true,
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"is_private": schema.StringAttribute{
						MarkdownDescription: "Whether the registry is private ('true' or 'false')",
						Optional:            true,
						Computed:            true,
					},
					"credentials": schema.StringAttribute{
						MarkdownDescription: "Name of the registry credentials resource",
						Optional:            true,
						Computed:            true,
					},
				},
			},
			"containers": schema.ListNestedAttribute{
				MarkdownDescription: "List of containers in the deployment",
				Required:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"image": schema.StringAttribute{
							MarkdownDescription: "Container image (e.g., 'nginx:latest')",
							Required:            true,
						},
						"exposed_port": schema.Int64Attribute{
							MarkdownDescription: "Port exposed by the container",
							Required:            true,
						},
						"healthcheck": schema.SingleNestedAttribute{
							MarkdownDescription: "Healthcheck configuration",
							Optional:            true,
							Attributes: map[string]schema.Attribute{
								"enabled": schema.StringAttribute{
									MarkdownDescription: "Whether healthcheck is enabled ('true' or 'false')",
									Required:            true,
								},
								"port": schema.StringAttribute{
									MarkdownDescription: "Port for healthcheck",
									Optional:            true,
								},
								"path": schema.StringAttribute{
									MarkdownDescription: "Path for healthcheck",
									Optional:            true,
								},
							},
						},
						"entrypoint_overrides": schema.SingleNestedAttribute{
							MarkdownDescription: "Override container entrypoint and command",
							Optional:            true,
							Attributes: map[string]schema.Attribute{
								"enabled": schema.BoolAttribute{
									MarkdownDescription: "Whether to override the entrypoint",
									Required:            true,
								},
								"entrypoint": schema.ListAttribute{
									MarkdownDescription: "Custom entrypoint array (e.g., [\"/bin/sh\", \"-c\"])",
									ElementType:         types.StringType,
									Optional:            true,
								},
								"cmd": schema.ListAttribute{
									MarkdownDescription: "Custom command array",
									ElementType:         types.StringType,
									Optional:            true,
								},
							},
						},
						"env": schema.ListNestedAttribute{
							MarkdownDescription: "Environment variables",
							Optional:            true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"type": schema.StringAttribute{
										MarkdownDescription: "Type of environment variable ('plain' or 'secret')",
										Required:            true,
									},
									"name": schema.StringAttribute{
										MarkdownDescription: "Name of the environment variable",
										Required:            true,
									},
									"value_or_reference_to_secret": schema.StringAttribute{
										MarkdownDescription: "Value for plain env vars or secret name for secret env vars",
										Required:            true,
									},
								},
							},
						},
						"volume_mounts": schema.ListNestedAttribute{
							MarkdownDescription: "Volume mounts for the container",
							Optional:            true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"type": schema.StringAttribute{
										MarkdownDescription: "Type of volume ('scratch', 'memory', 'secret', 'shared')",
										Required:            true,
									},
									"mount_path": schema.StringAttribute{
										MarkdownDescription: "Path where volume will be mounted in container",
										Required:            true,
									},
									"secret_name": schema.StringAttribute{
										MarkdownDescription: "Name of secret (required for type='secret')",
										Optional:            true,
									},
									"size_in_mb": schema.Int64Attribute{
										MarkdownDescription: "Size in MB (optional for type='scratch' or 'memory')",
										Optional:            true,
									},
									"volume_id": schema.StringAttribute{
										MarkdownDescription: "Volume ID (required for type='shared')",
										Optional:            true,
									},
								},
								Validators: []validator.Object{
									volumeMountValidator{},
								},
							},
						},
					},
				},
			},
			"endpoint_base_url": schema.StringAttribute{
				MarkdownDescription: "Base URL for the deployment endpoint",
				Computed:            true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "Timestamp when the deployment was created",
				Computed:            true,
			},
		},
	}
}

func (r *ContainerResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*verda.Client)

	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *verda.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	r.client = client
}

func (r *ContainerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ContainerResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	createReq := &verda.CreateDeploymentRequest{
		Name:   data.Name.ValueString(),
		IsSpot: data.IsSpot.ValueBool(),
	}

	// Parse compute
	var compute ComputeModel
	resp.Diagnostics.Append(data.Compute.As(ctx, &compute, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq.Compute = verda.ContainerCompute{
		Name: compute.Name.ValueString(),
		Size: int(compute.Size.ValueInt64()),
	}

	// Parse scaling
	var scaling ScalingModel
	resp.Diagnostics.Append(data.Scaling.As(ctx, &scaling, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	scalingOptions := verda.ContainerScalingOptions{
		MinReplicaCount:              int(scaling.MinReplicaCount.ValueInt64()),
		MaxReplicaCount:              int(scaling.MaxReplicaCount.ValueInt64()),
		QueueMessageTTLSeconds:       int(scaling.QueueMessageTTLSeconds.ValueInt64()),
		ConcurrentRequestsPerReplica: int(scaling.ConcurrentRequestsPerReplica.ValueInt64()),
	}

	// Parse scale down policy
	var scaleDownPolicy ScalingPolicyModel
	resp.Diagnostics.Append(scaling.ScaleDownPolicy.As(ctx, &scaleDownPolicy, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}
	scalingOptions.ScaleDownPolicy = &verda.ScalingPolicy{
		DelaySeconds: int(scaleDownPolicy.DelaySeconds.ValueInt64()),
	}

	// Parse scale up policy
	var scaleUpPolicy ScalingPolicyModel
	resp.Diagnostics.Append(scaling.ScaleUpPolicy.As(ctx, &scaleUpPolicy, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}
	scalingOptions.ScaleUpPolicy = &verda.ScalingPolicy{
		DelaySeconds: int(scaleUpPolicy.DelaySeconds.ValueInt64()),
	}

	// Parse queue load trigger
	var queueLoad QueueLoadTriggerModel
	resp.Diagnostics.Append(scaling.QueueLoad.As(ctx, &queueLoad, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}
	scalingOptions.ScalingTriggers = &verda.ScalingTriggers{
		QueueLoad: &verda.QueueLoadTrigger{
			Threshold: queueLoad.Threshold.ValueFloat64(),
		},
	}

	createReq.Scaling = scalingOptions

	// Parse container registry settings if provided
	if !data.ContainerRegistrySettings.IsNull() && !data.ContainerRegistrySettings.IsUnknown() {
		var registrySettings RegistrySettingsModel
		resp.Diagnostics.Append(data.ContainerRegistrySettings.As(ctx, &registrySettings, basetypes.ObjectAsOptions{})...)
		if resp.Diagnostics.HasError() {
			return
		}

		isPrivate := registrySettings.IsPrivate.ValueString() == "true"
		createReq.ContainerRegistrySettings = verda.ContainerRegistrySettings{
			IsPrivate: isPrivate,
		}

		if !registrySettings.Credentials.IsNull() && registrySettings.Credentials.ValueString() != "" {
			createReq.ContainerRegistrySettings.Credentials = &verda.RegistryCredentialsRef{
				Name: registrySettings.Credentials.ValueString(),
			}
		}
	} else {
		// By default, we'll assume the registry is public
		createReq.ContainerRegistrySettings = verda.ContainerRegistrySettings{
			IsPrivate: false,
		}
	}

	// Parse containers
	var containers []ContainerModel
	resp.Diagnostics.Append(data.Containers.ElementsAs(ctx, &containers, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var deploymentContainers []verda.CreateDeploymentContainer
	for _, container := range containers {
		deploymentContainer := verda.CreateDeploymentContainer{
			Image:       container.Image.ValueString(),
			ExposedPort: int(container.ExposedPort.ValueInt64()),
		}

		// Parse healthcheck if provided
		if !container.Healthcheck.IsNull() {
			var healthcheck HealthcheckModel
			resp.Diagnostics.Append(container.Healthcheck.As(ctx, &healthcheck, basetypes.ObjectAsOptions{})...)
			if resp.Diagnostics.HasError() {
				return
			}

			enabled := healthcheck.Enabled.ValueString() == "true"
			hc := &verda.ContainerHealthcheck{
				Enabled: enabled,
			}

			if !healthcheck.Port.IsNull() && healthcheck.Port.ValueString() != "" {
				var port int
				_, err := fmt.Sscanf(healthcheck.Port.ValueString(), "%d", &port)
				if err == nil {
					hc.Port = port
				}
			}

			if !healthcheck.Path.IsNull() {
				hc.Path = healthcheck.Path.ValueString()
			}

			deploymentContainer.Healthcheck = hc
		}

		// Parse entrypoint overrides if provided
		if !container.EntrypointOverrides.IsNull() && !container.EntrypointOverrides.IsUnknown() {
			var entrypointOverrides EntrypointOverridesModel
			resp.Diagnostics.Append(container.EntrypointOverrides.As(ctx, &entrypointOverrides, basetypes.ObjectAsOptions{})...)
			if resp.Diagnostics.HasError() {
				return
			}

			overrides := &verda.ContainerEntrypointOverrides{
				Enabled: entrypointOverrides.Enabled.ValueBool(),
			}

			if !entrypointOverrides.Entrypoint.IsNull() && !entrypointOverrides.Entrypoint.IsUnknown() {
				var entrypoint []string
				resp.Diagnostics.Append(entrypointOverrides.Entrypoint.ElementsAs(ctx, &entrypoint, false)...)
				if resp.Diagnostics.HasError() {
					return
				}
				overrides.Entrypoint = entrypoint
			}

			if !entrypointOverrides.Cmd.IsNull() && !entrypointOverrides.Cmd.IsUnknown() {
				var cmd []string
				resp.Diagnostics.Append(entrypointOverrides.Cmd.ElementsAs(ctx, &cmd, false)...)
				if resp.Diagnostics.HasError() {
					return
				}
				overrides.Cmd = cmd
			}

			deploymentContainer.EntrypointOverrides = overrides
		}

		// Parse environment variables if provided
		if !container.Env.IsNull() {
			var envVars []EnvVarModel
			resp.Diagnostics.Append(container.Env.ElementsAs(ctx, &envVars, false)...)
			if resp.Diagnostics.HasError() {
				return
			}

			var containerEnvVars []verda.ContainerEnvVar
			for _, envVar := range envVars {
				containerEnvVars = append(containerEnvVars, verda.ContainerEnvVar{
					Type:                     envVar.Type.ValueString(),
					Name:                     envVar.Name.ValueString(),
					ValueOrReferenceToSecret: envVar.ValueOrReferenceToSecret.ValueString(),
				})
			}
			deploymentContainer.Env = containerEnvVars
		}

		// Parse volume mounts if provided
		if !container.VolumeMounts.IsNull() {
			var volumeMounts []VolumeMountModel
			resp.Diagnostics.Append(container.VolumeMounts.ElementsAs(ctx, &volumeMounts, false)...)
			if resp.Diagnostics.HasError() {
				return
			}

			var containerVolumeMounts []verda.ContainerVolumeMount
			for _, volumeMount := range volumeMounts {
				mount := verda.ContainerVolumeMount{
					Type:      volumeMount.Type.ValueString(),
					MountPath: volumeMount.MountPath.ValueString(),
				}

				if !volumeMount.SecretName.IsNull() && volumeMount.SecretName.ValueString() != "" {
					mount.SecretName = volumeMount.SecretName.ValueString()
				}

				if !volumeMount.SizeInMB.IsNull() {
					mount.SizeInMB = int(volumeMount.SizeInMB.ValueInt64())
				}

				if !volumeMount.VolumeID.IsNull() && volumeMount.VolumeID.ValueString() != "" {
					mount.VolumeID = volumeMount.VolumeID.ValueString()
				}

				containerVolumeMounts = append(containerVolumeMounts, mount)
			}
			deploymentContainer.VolumeMounts = containerVolumeMounts
		}

		deploymentContainers = append(deploymentContainers, deploymentContainer)
	}

	createReq.Containers = deploymentContainers

	deployment, err := r.client.ContainerDeployments.CreateDeployment(ctx, createReq)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create container deployment, got error: %s", err))
		return
	}

	// Flatten API response, merging with plan to preserve fields the API doesn't return
	planContainers := data.Containers
	r.flattenDeploymentToModel(ctx, deployment, &data, &resp.Diagnostics)
	// Merge API response with plan to preserve fields the API doesn't echo back
	r.mergeContainersFromPlan(ctx, planContainers, &data, &resp.Diagnostics)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ContainerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ContainerResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	deployment, err := r.client.ContainerDeployments.GetDeploymentByName(ctx, data.Name.ValueString())
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read container deployment, got error: %s", err))
		return
	}

	// Also fetch scaling configuration
	scalingConfig, err := r.client.ContainerDeployments.GetDeploymentScaling(ctx, data.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read scaling configuration, got error: %s", err))
		return
	}

	// Preserve container configuration from prior state
	// The API doesn't return all fields (like volume_id for non-shared volumes)
	priorContainers := data.Containers
	r.flattenDeploymentToModel(ctx, deployment, &data, &resp.Diagnostics)
	r.flattenScalingToModel(ctx, scalingConfig, &data, &resp.Diagnostics)
	// Merge API response with prior state to preserve fields the API doesn't return
	r.mergeContainersFromPlan(ctx, priorContainers, &data, &resp.Diagnostics)

	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ContainerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data ContainerResourceModel
	var prior ContainerResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)

	if resp.Diagnostics.HasError() {
		return
	}

	patch := &verda.UpdateDeploymentRequest{}
	updateDeployment := false
	if !data.IsSpot.Equal(prior.IsSpot) {
		isSpot := data.IsSpot.ValueBool()
		patch.IsSpot = &isSpot
		updateDeployment = true
	}
	if !data.Compute.Equal(prior.Compute) {
		var compute ComputeModel
		resp.Diagnostics.Append(data.Compute.As(ctx, &compute, basetypes.ObjectAsOptions{})...)
		patch.Compute = &verda.ContainerCompute{Name: compute.Name.ValueString(), Size: int(compute.Size.ValueInt64())}
		updateDeployment = true
	}
	if !data.ContainerRegistrySettings.Equal(prior.ContainerRegistrySettings) {
		patch.ContainerRegistrySettings = &verda.ContainerRegistrySettings{IsPrivate: false}
		if !data.ContainerRegistrySettings.IsNull() {
			var registry RegistrySettingsModel
			resp.Diagnostics.Append(data.ContainerRegistrySettings.As(ctx, &registry, basetypes.ObjectAsOptions{})...)
			patch.ContainerRegistrySettings.IsPrivate = registry.IsPrivate.ValueString() == "true"
			if !registry.Credentials.IsNull() && registry.Credentials.ValueString() != "" {
				patch.ContainerRegistrySettings.Credentials = &verda.RegistryCredentialsRef{Name: registry.Credentials.ValueString()}
			}
		}
		updateDeployment = true
	}
	if !data.Containers.Equal(prior.Containers) {
		live, err := r.client.ContainerDeployments.GetDeploymentByName(ctx, data.Name.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read container names before update: %s", err))
			return
		}
		var planned []ContainerModel
		var previous []ContainerModel
		resp.Diagnostics.Append(data.Containers.ElementsAs(ctx, &planned, false)...)
		resp.Diagnostics.Append(prior.Containers.ElementsAs(ctx, &previous, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if len(planned) != len(live.Containers) || len(planned) != len(previous) {
			resp.Diagnostics.AddError("Container count change not supported", "Verda's PATCH requires existing container names. To add or remove containers, replace the deployment.")
			return
		}
		for i, container := range planned {
			if container.Image.Equal(previous[i].Image) &&
				container.ExposedPort.Equal(previous[i].ExposedPort) &&
				container.Healthcheck.Equal(previous[i].Healthcheck) &&
				container.EntrypointOverrides.Equal(previous[i].EntrypointOverrides) &&
				container.Env.Equal(previous[i].Env) &&
				container.VolumeMounts.Equal(previous[i].VolumeMounts) {
				continue
			}
			updated := verda.CreateDeploymentContainer{
				Name:  live.Containers[i].Name,
				Image: container.Image.ValueString(),
			}
			if !container.ExposedPort.Equal(previous[i].ExposedPort) {
				updated.ExposedPort = int(container.ExposedPort.ValueInt64())
			}
			if !container.Healthcheck.Equal(previous[i].Healthcheck) && !container.Healthcheck.IsNull() {
				var hc HealthcheckModel
				resp.Diagnostics.Append(container.Healthcheck.As(ctx, &hc, basetypes.ObjectAsOptions{})...)
				updated.Healthcheck = &verda.ContainerHealthcheck{Enabled: hc.Enabled.ValueString() == "true", Path: hc.Path.ValueString()}
				if !hc.Port.IsNull() && hc.Port.ValueString() != "" {
					if _, err := fmt.Sscanf(hc.Port.ValueString(), "%d", &updated.Healthcheck.Port); err != nil {
						resp.Diagnostics.AddError("Invalid healthcheck port", err.Error())
					}
				}
			} else if !container.Healthcheck.Equal(previous[i].Healthcheck) {
				updated.Healthcheck = &verda.ContainerHealthcheck{Enabled: false}
			}
			if !container.EntrypointOverrides.Equal(previous[i].EntrypointOverrides) && !container.EntrypointOverrides.IsNull() {
				var overrides EntrypointOverridesModel
				resp.Diagnostics.Append(container.EntrypointOverrides.As(ctx, &overrides, basetypes.ObjectAsOptions{})...)
				updated.EntrypointOverrides = &verda.ContainerEntrypointOverrides{Enabled: overrides.Enabled.ValueBool()}
				if !overrides.Entrypoint.IsNull() {
					resp.Diagnostics.Append(overrides.Entrypoint.ElementsAs(ctx, &updated.EntrypointOverrides.Entrypoint, false)...)
				}
				if !overrides.Cmd.IsNull() {
					resp.Diagnostics.Append(overrides.Cmd.ElementsAs(ctx, &updated.EntrypointOverrides.Cmd, false)...)
				}
			} else if !container.EntrypointOverrides.Equal(previous[i].EntrypointOverrides) {
				updated.EntrypointOverrides = &verda.ContainerEntrypointOverrides{Enabled: false}
			}
			if !container.Env.Equal(previous[i].Env) && !container.Env.IsNull() {
				var env []EnvVarModel
				resp.Diagnostics.Append(container.Env.ElementsAs(ctx, &env, false)...)
				updated.Env = make([]verda.ContainerEnvVar, 0, len(env))
				for _, value := range env {
					updated.Env = append(updated.Env, verda.ContainerEnvVar{Type: value.Type.ValueString(), Name: value.Name.ValueString(), ValueOrReferenceToSecret: value.ValueOrReferenceToSecret.ValueString()})
				}
				if len(env) == 0 && len(live.Containers[i].Env) > 0 {
					resp.Diagnostics.AddError("Cannot clear environment variables", "The Verda Go SDK omits empty environment lists in deployment PATCH requests. Remove environment variables through Verda before applying this plan.")
				}
			} else if !container.Env.Equal(previous[i].Env) && len(live.Containers[i].Env) > 0 {
				resp.Diagnostics.AddError("Cannot clear environment variables", "The Verda Go SDK omits empty environment lists in deployment PATCH requests. Remove environment variables through Verda before applying this plan.")
			}
			if !container.VolumeMounts.Equal(previous[i].VolumeMounts) && !container.VolumeMounts.IsNull() {
				var mounts []VolumeMountModel
				resp.Diagnostics.Append(container.VolumeMounts.ElementsAs(ctx, &mounts, false)...)
				updated.VolumeMounts = make([]verda.ContainerVolumeMount, 0, len(mounts))
				for _, mount := range mounts {
					updated.VolumeMounts = append(updated.VolumeMounts, verda.ContainerVolumeMount{Type: mount.Type.ValueString(), MountPath: mount.MountPath.ValueString(), SecretName: mount.SecretName.ValueString(), SizeInMB: int(mount.SizeInMB.ValueInt64()), VolumeID: mount.VolumeID.ValueString()})
				}
				if len(mounts) == 0 && len(live.Containers[i].VolumeMounts) > 0 {
					resp.Diagnostics.AddError("Cannot clear volume mounts", "The Verda Go SDK omits empty volume mount lists in deployment PATCH requests. Replace the deployment to remove all mounts.")
				}
			} else if !container.VolumeMounts.Equal(previous[i].VolumeMounts) && len(live.Containers[i].VolumeMounts) > 0 {
				resp.Diagnostics.AddError("Cannot clear volume mounts", "The Verda Go SDK omits empty volume mount lists in deployment PATCH requests. Replace the deployment to remove all mounts.")
			}
			patch.Containers = append(patch.Containers, updated)
		}
		updateDeployment = updateDeployment || len(patch.Containers) > 0
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if updateDeployment {
		if _, err := r.client.ContainerDeployments.UpdateDeployment(ctx, data.Name.ValueString(), patch); err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update container deployment: %s", err))
			return
		}
	}
	if !data.Scaling.Equal(prior.Scaling) {
		updateScaling(ctx, r.client, data.Name.ValueString(), data.Scaling, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	deployment, err := r.client.ContainerDeployments.GetDeploymentByName(ctx, data.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read updated container deployment: %s", err))
		return
	}
	scaling, err := r.client.ContainerDeployments.GetDeploymentScaling(ctx, data.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read updated scaling: %s", err))
		return
	}
	plannedContainers := data.Containers
	r.flattenDeploymentToModel(ctx, deployment, &data, &resp.Diagnostics)
	r.flattenScalingToModel(ctx, scaling, &data, &resp.Diagnostics)
	r.mergeContainersFromPlan(ctx, plannedContainers, &data, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	}
}

func updateScaling(ctx context.Context, client *verda.Client, name string, value types.Object, diagnostics *diag.Diagnostics) {
	patch := buildScalingPatch(ctx, value, diagnostics)
	if diagnostics.HasError() {
		return
	}
	if _, err := client.ContainerDeployments.UpdateDeploymentScaling(ctx, name, patch); err != nil {
		diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update deployment scaling: %s", err))
	}
}

func buildScalingPatch(ctx context.Context, value types.Object, diagnostics *diag.Diagnostics) *verda.UpdateScalingOptionsRequest {
	var scaling ScalingModel
	diagnostics.Append(value.As(ctx, &scaling, basetypes.ObjectAsOptions{})...)
	if diagnostics.HasError() {
		return nil
	}
	minReplicas := int(scaling.MinReplicaCount.ValueInt64())
	maxReplicas := int(scaling.MaxReplicaCount.ValueInt64())
	ttl := int(scaling.QueueMessageTTLSeconds.ValueInt64())
	concurrency := int(scaling.ConcurrentRequestsPerReplica.ValueInt64())
	patch := &verda.UpdateScalingOptionsRequest{
		MinReplicaCount: &minReplicas, MaxReplicaCount: &maxReplicas,
		QueueMessageTTLSeconds: &ttl, ConcurrentRequestsPerReplica: &concurrency,
	}
	var down, up ScalingPolicyModel
	var queue QueueLoadTriggerModel
	diagnostics.Append(scaling.ScaleDownPolicy.As(ctx, &down, basetypes.ObjectAsOptions{})...)
	diagnostics.Append(scaling.ScaleUpPolicy.As(ctx, &up, basetypes.ObjectAsOptions{})...)
	diagnostics.Append(scaling.QueueLoad.As(ctx, &queue, basetypes.ObjectAsOptions{})...)
	if diagnostics.HasError() {
		return nil
	}
	patch.ScaleDownPolicy = &verda.ScalingPolicy{DelaySeconds: int(down.DelaySeconds.ValueInt64())}
	patch.ScaleUpPolicy = &verda.ScalingPolicy{DelaySeconds: int(up.DelaySeconds.ValueInt64())}
	patch.ScalingTriggers = &verda.ScalingTriggers{QueueLoad: &verda.QueueLoadTrigger{Threshold: queue.Threshold.ValueFloat64()}}
	return patch
}

func (r *ContainerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ContainerResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Initiate deletion (ignore timeout errors as we'll poll instead)
	err := r.client.ContainerDeployments.DeleteDeployment(ctx, data.Name.ValueString(), 60000)
	if isNotFound(err) {
		return
	}
	if err != nil && !isTimeoutError(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete container deployment, got error: %s", err))
		return
	}

	// Poll until deployment is gone (404) with 5 minute timeout
	if err := r.waitForDeletionComplete(ctx, data.Name.ValueString(), 300); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Timeout waiting for container deployment deletion: %s", err))
		return
	}
}

func (r *ContainerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func isNotFound(err error) bool {
	var apiErr *verda.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == 404
}

func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *verda.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == 504 {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout")
}

func (r *ContainerResource) waitForDeletionComplete(ctx context.Context, deploymentName string, timeoutSeconds int) error {
	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)

	for time.Now().Before(deadline) {
		// Check if context was cancelled
		if ctx.Err() != nil {
			return fmt.Errorf("context cancelled: %w", ctx.Err())
		}

		// Try to get the deployment
		_, err := r.client.ContainerDeployments.GetDeploymentByName(ctx, deploymentName)
		if err != nil {
			if isNotFound(err) {
				return nil
			}
			// For other errors, continue polling (deployment might be in transition)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}

	return fmt.Errorf("timeout after %d seconds waiting for deployment deletion", timeoutSeconds)
}

func (r *ContainerResource) flattenDeploymentToModel(ctx context.Context, deployment *verda.ContainerDeployment, data *ContainerResourceModel, diagnostics *diag.Diagnostics) {
	data.ID = types.StringValue(deployment.Name)
	data.Name = types.StringValue(deployment.Name)
	data.IsSpot = types.BoolValue(deployment.IsSpot)
	data.EndpointBaseURL = types.StringValue(deployment.EndpointBaseURL)
	data.CreatedAt = types.StringValue(deployment.CreatedAt.Format("2006-01-02T15:04:05Z"))

	// Flatten compute
	if deployment.Compute != nil {
		computeObj, diags := types.ObjectValue(
			map[string]attr.Type{
				"name": types.StringType,
				"size": types.Int64Type,
			},
			map[string]attr.Value{
				"name": types.StringValue(deployment.Compute.Name),
				"size": types.Int64Value(int64(deployment.Compute.Size)),
			},
		)
		diagnostics.Append(diags...)
		data.Compute = computeObj
	}

	// Flatten container registry settings
	if deployment.ContainerRegistrySettings != nil {
		registryAttrTypes := map[string]attr.Type{
			"is_private":  types.StringType,
			"credentials": types.StringType,
		}

		registryAttrValues := map[string]attr.Value{
			"is_private": types.StringValue(fmt.Sprintf("%t", deployment.ContainerRegistrySettings.IsPrivate)),
		}

		if deployment.ContainerRegistrySettings.Credentials != nil {
			registryAttrValues["credentials"] = types.StringValue(deployment.ContainerRegistrySettings.Credentials.Name)
		} else {
			registryAttrValues["credentials"] = types.StringNull()
		}

		registryObj, diags := types.ObjectValue(registryAttrTypes, registryAttrValues)
		diagnostics.Append(diags...)
		data.ContainerRegistrySettings = registryObj
	}

	// Flatten containers - only include user-specified data, filter out API-added mounts
	r.flattenContainersToModel(ctx, deployment.Containers, data, diagnostics)
}

// mergeContainersFromPlan merges plan/state container data with API response
// This preserves fields that the API doesn't return (like volume_id for non-shared volumes)
func (r *ContainerResource) mergeContainersFromPlan(ctx context.Context, planContainers types.List, data *ContainerResourceModel, diagnostics *diag.Diagnostics) {
	if planContainers.IsNull() || planContainers.IsUnknown() {
		return
	}

	var planContainersList []ContainerModel
	diags := planContainers.ElementsAs(ctx, &planContainersList, false)
	diagnostics.Append(diags...)
	if diagnostics.HasError() {
		return
	}

	var apiContainersList []ContainerModel
	if !data.Containers.IsNull() && !data.Containers.IsUnknown() {
		diags = data.Containers.ElementsAs(ctx, &apiContainersList, false)
		diagnostics.Append(diags...)
		if diagnostics.HasError() {
			return
		}
	}

	// If API didn't return containers or returned fewer containers, use plan as-is
	if len(apiContainersList) == 0 || len(apiContainersList) != len(planContainersList) {
		data.Containers = planContainers
		return
	}

	// Merge each container: use API data where available, fill in from plan where not
	var mergedContainers []attr.Value
	for i := range planContainersList {
		if i >= len(apiContainersList) {
			break
		}

		planContainer := planContainersList[i]
		apiContainer := apiContainersList[i]

		// For volume_mounts, merge carefully
		// Use plan volume_mounts since API doesn't return all fields (like volume_id for non-shared)
		mergedContainer := apiContainer
		mergedContainer.VolumeMounts = planContainer.VolumeMounts
		if planContainer.Healthcheck.IsNull() {
			mergedContainer.Healthcheck = planContainer.Healthcheck
		}
		if planContainer.Env.IsNull() {
			mergedContainer.Env = planContainer.Env
		}

		// Unconfigured entrypoints are not managed; preserve configured values if the API omits them.
		if planContainer.EntrypointOverrides.IsNull() ||
			apiContainer.EntrypointOverrides.IsNull() || apiContainer.EntrypointOverrides.IsUnknown() {
			mergedContainer.EntrypointOverrides = planContainer.EntrypointOverrides
		}

		// Convert back to attr.Value
		containerAttrTypes := map[string]attr.Type{
			"image":        types.StringType,
			"exposed_port": types.Int64Type,
			"healthcheck": types.ObjectType{
				AttrTypes: map[string]attr.Type{
					"enabled": types.StringType,
					"port":    types.StringType,
					"path":    types.StringType,
				},
			},
			"entrypoint_overrides": types.ObjectType{
				AttrTypes: map[string]attr.Type{
					"enabled":    types.BoolType,
					"entrypoint": types.ListType{ElemType: types.StringType},
					"cmd":        types.ListType{ElemType: types.StringType},
				},
			},
			"env": types.ListType{
				ElemType: types.ObjectType{
					AttrTypes: map[string]attr.Type{
						"type":                         types.StringType,
						"name":                         types.StringType,
						"value_or_reference_to_secret": types.StringType,
					},
				},
			},
			"volume_mounts": types.ListType{
				ElemType: types.ObjectType{
					AttrTypes: map[string]attr.Type{
						"type":        types.StringType,
						"mount_path":  types.StringType,
						"secret_name": types.StringType,
						"size_in_mb":  types.Int64Type,
						"volume_id":   types.StringType,
					},
				},
			},
		}

		containerAttrValues := map[string]attr.Value{
			"image":                mergedContainer.Image,
			"exposed_port":         mergedContainer.ExposedPort,
			"healthcheck":          mergedContainer.Healthcheck,
			"entrypoint_overrides": mergedContainer.EntrypointOverrides,
			"env":                  mergedContainer.Env,
			"volume_mounts":        mergedContainer.VolumeMounts,
		}

		containerObj, diags := types.ObjectValue(containerAttrTypes, containerAttrValues)
		diagnostics.Append(diags...)
		mergedContainers = append(mergedContainers, containerObj)
	}

	// Create the merged containers list
	containersList, diags := types.ListValue(
		types.ObjectType{
			AttrTypes: map[string]attr.Type{
				"image":        types.StringType,
				"exposed_port": types.Int64Type,
				"healthcheck": types.ObjectType{
					AttrTypes: map[string]attr.Type{
						"enabled": types.StringType,
						"port":    types.StringType,
						"path":    types.StringType,
					},
				},
				"entrypoint_overrides": types.ObjectType{
					AttrTypes: map[string]attr.Type{
						"enabled":    types.BoolType,
						"entrypoint": types.ListType{ElemType: types.StringType},
						"cmd":        types.ListType{ElemType: types.StringType},
					},
				},
				"env": types.ListType{
					ElemType: types.ObjectType{
						AttrTypes: map[string]attr.Type{
							"type":                         types.StringType,
							"name":                         types.StringType,
							"value_or_reference_to_secret": types.StringType,
						},
					},
				},
				"volume_mounts": types.ListType{
					ElemType: types.ObjectType{
						AttrTypes: map[string]attr.Type{
							"type":        types.StringType,
							"mount_path":  types.StringType,
							"secret_name": types.StringType,
							"size_in_mb":  types.Int64Type,
							"volume_id":   types.StringType,
						},
					},
				},
			},
		},
		mergedContainers,
	)
	diagnostics.Append(diags...)
	data.Containers = containersList
}

func (r *ContainerResource) flattenContainersToModel(ctx context.Context, containers []verda.DeploymentContainer, data *ContainerResourceModel, diagnostics *diag.Diagnostics) {
	if len(containers) == 0 {
		return
	}

	var containerElements []attr.Value

	for _, container := range containers {
		// Build healthcheck object if present and enabled
		// Only include healthcheck in state if it's actually enabled
		var healthcheckObj types.Object
		if container.Healthcheck != nil && container.Healthcheck.Enabled {
			healthcheckAttrTypes := map[string]attr.Type{
				"enabled": types.StringType,
				"port":    types.StringType,
				"path":    types.StringType,
			}

			healthcheckAttrValues := map[string]attr.Value{
				"enabled": types.StringValue(fmt.Sprintf("%t", container.Healthcheck.Enabled)),
			}

			if container.Healthcheck.Port != 0 {
				healthcheckAttrValues["port"] = types.StringValue(fmt.Sprintf("%d", container.Healthcheck.Port))
			} else {
				healthcheckAttrValues["port"] = types.StringNull()
			}

			if container.Healthcheck.Path != "" {
				healthcheckAttrValues["path"] = types.StringValue(container.Healthcheck.Path)
			} else {
				healthcheckAttrValues["path"] = types.StringNull()
			}

			hcObj, diags := types.ObjectValue(healthcheckAttrTypes, healthcheckAttrValues)
			diagnostics.Append(diags...)
			healthcheckObj = hcObj
		} else {
			healthcheckObj = types.ObjectNull(map[string]attr.Type{
				"enabled": types.StringType,
				"port":    types.StringType,
				"path":    types.StringType,
			})
		}

		// Build entrypoint overrides object if present
		var entrypointOverridesObj types.Object
		if container.EntrypointOverrides != nil && container.EntrypointOverrides.Enabled {
			entrypointList, diags := types.ListValueFrom(ctx, types.StringType, container.EntrypointOverrides.Entrypoint)
			diagnostics.Append(diags...)

			cmdList, diags := types.ListValueFrom(ctx, types.StringType, container.EntrypointOverrides.Cmd)
			diagnostics.Append(diags...)

			entrypointOverridesAttrTypes := map[string]attr.Type{
				"enabled":    types.BoolType,
				"entrypoint": types.ListType{ElemType: types.StringType},
				"cmd":        types.ListType{ElemType: types.StringType},
			}

			entrypointOverridesAttrValues := map[string]attr.Value{
				"enabled":    types.BoolValue(container.EntrypointOverrides.Enabled),
				"entrypoint": entrypointList,
				"cmd":        cmdList,
			}

			epObj, diags := types.ObjectValue(entrypointOverridesAttrTypes, entrypointOverridesAttrValues)
			diagnostics.Append(diags...)
			entrypointOverridesObj = epObj
		} else {
			entrypointOverridesObj = types.ObjectNull(map[string]attr.Type{
				"enabled":    types.BoolType,
				"entrypoint": types.ListType{ElemType: types.StringType},
				"cmd":        types.ListType{ElemType: types.StringType},
			})
		}

		// Build env vars list
		var envList types.List
		if len(container.Env) > 0 {
			var envElements []attr.Value
			for _, envVar := range container.Env {
				envAttrTypes := map[string]attr.Type{
					"type":                         types.StringType,
					"name":                         types.StringType,
					"value_or_reference_to_secret": types.StringType,
				}

				envAttrValues := map[string]attr.Value{
					"type":                         types.StringValue(envVar.Type),
					"name":                         types.StringValue(envVar.Name),
					"value_or_reference_to_secret": types.StringValue(envVar.ValueOrReferenceToSecret),
				}

				envObj, diags := types.ObjectValue(envAttrTypes, envAttrValues)
				diagnostics.Append(diags...)
				envElements = append(envElements, envObj)
			}

			envListVal, diags := types.ListValue(
				types.ObjectType{
					AttrTypes: map[string]attr.Type{
						"type":                         types.StringType,
						"name":                         types.StringType,
						"value_or_reference_to_secret": types.StringType,
					},
				},
				envElements,
			)
			diagnostics.Append(diags...)
			envList = envListVal
		} else {
			envList = types.ListNull(types.ObjectType{
				AttrTypes: map[string]attr.Type{
					"type":                         types.StringType,
					"name":                         types.StringType,
					"value_or_reference_to_secret": types.StringType,
				},
			})
		}

		// Build volume mounts list
		// Note: We don't filter API-added mounts here because the merge function
		// preserves volume_mounts from plan/state, which already has the correct data
		var volumeMountsList types.List
		if len(container.VolumeMounts) > 0 {
			var volumeMountElements []attr.Value
			for _, mount := range container.VolumeMounts {
				volumeMountAttrTypes := map[string]attr.Type{
					"type":        types.StringType,
					"mount_path":  types.StringType,
					"secret_name": types.StringType,
					"size_in_mb":  types.Int64Type,
					"volume_id":   types.StringType,
				}

				volumeMountAttrValues := map[string]attr.Value{
					"type":       types.StringValue(mount.Type),
					"mount_path": types.StringValue(mount.MountPath),
				}

				if mount.SecretName != "" {
					volumeMountAttrValues["secret_name"] = types.StringValue(mount.SecretName)
				} else {
					volumeMountAttrValues["secret_name"] = types.StringNull()
				}

				if mount.SizeInMB != 0 {
					volumeMountAttrValues["size_in_mb"] = types.Int64Value(int64(mount.SizeInMB))
				} else {
					volumeMountAttrValues["size_in_mb"] = types.Int64Null()
				}

				if mount.VolumeID != "" {
					volumeMountAttrValues["volume_id"] = types.StringValue(mount.VolumeID)
				} else {
					volumeMountAttrValues["volume_id"] = types.StringNull()
				}

				volumeMountObj, diags := types.ObjectValue(volumeMountAttrTypes, volumeMountAttrValues)
				diagnostics.Append(diags...)
				volumeMountElements = append(volumeMountElements, volumeMountObj)
			}

			volumeMountsListVal, diags := types.ListValue(
				types.ObjectType{
					AttrTypes: map[string]attr.Type{
						"type":        types.StringType,
						"mount_path":  types.StringType,
						"secret_name": types.StringType,
						"size_in_mb":  types.Int64Type,
						"volume_id":   types.StringType,
					},
				},
				volumeMountElements,
			)
			diagnostics.Append(diags...)
			volumeMountsList = volumeMountsListVal
		} else {
			volumeMountsList = types.ListNull(types.ObjectType{
				AttrTypes: map[string]attr.Type{
					"type":        types.StringType,
					"mount_path":  types.StringType,
					"secret_name": types.StringType,
					"size_in_mb":  types.Int64Type,
					"volume_id":   types.StringType,
				},
			})
		}

		// Build the container object
		containerAttrTypes := map[string]attr.Type{
			"image":        types.StringType,
			"exposed_port": types.Int64Type,
			"healthcheck": types.ObjectType{
				AttrTypes: map[string]attr.Type{
					"enabled": types.StringType,
					"port":    types.StringType,
					"path":    types.StringType,
				},
			},
			"entrypoint_overrides": types.ObjectType{
				AttrTypes: map[string]attr.Type{
					"enabled":    types.BoolType,
					"entrypoint": types.ListType{ElemType: types.StringType},
					"cmd":        types.ListType{ElemType: types.StringType},
				},
			},
			"env": types.ListType{
				ElemType: types.ObjectType{
					AttrTypes: map[string]attr.Type{
						"type":                         types.StringType,
						"name":                         types.StringType,
						"value_or_reference_to_secret": types.StringType,
					},
				},
			},
			"volume_mounts": types.ListType{
				ElemType: types.ObjectType{
					AttrTypes: map[string]attr.Type{
						"type":        types.StringType,
						"mount_path":  types.StringType,
						"secret_name": types.StringType,
						"size_in_mb":  types.Int64Type,
						"volume_id":   types.StringType,
					},
				},
			},
		}

		containerAttrValues := map[string]attr.Value{
			"image":                types.StringValue(container.Image.Image),
			"exposed_port":         types.Int64Value(int64(container.ExposedPort)),
			"healthcheck":          healthcheckObj,
			"entrypoint_overrides": entrypointOverridesObj,
			"env":                  envList,
			"volume_mounts":        volumeMountsList,
		}

		containerObj, diags := types.ObjectValue(containerAttrTypes, containerAttrValues)
		diagnostics.Append(diags...)
		containerElements = append(containerElements, containerObj)
	}

	// Create the containers list
	containersList, diags := types.ListValue(
		types.ObjectType{
			AttrTypes: map[string]attr.Type{
				"image":        types.StringType,
				"exposed_port": types.Int64Type,
				"healthcheck": types.ObjectType{
					AttrTypes: map[string]attr.Type{
						"enabled": types.StringType,
						"port":    types.StringType,
						"path":    types.StringType,
					},
				},
				"entrypoint_overrides": types.ObjectType{
					AttrTypes: map[string]attr.Type{
						"enabled":    types.BoolType,
						"entrypoint": types.ListType{ElemType: types.StringType},
						"cmd":        types.ListType{ElemType: types.StringType},
					},
				},
				"env": types.ListType{
					ElemType: types.ObjectType{
						AttrTypes: map[string]attr.Type{
							"type":                         types.StringType,
							"name":                         types.StringType,
							"value_or_reference_to_secret": types.StringType,
						},
					},
				},
				"volume_mounts": types.ListType{
					ElemType: types.ObjectType{
						AttrTypes: map[string]attr.Type{
							"type":        types.StringType,
							"mount_path":  types.StringType,
							"secret_name": types.StringType,
							"size_in_mb":  types.Int64Type,
							"volume_id":   types.StringType,
						},
					},
				},
			},
		},
		containerElements,
	)
	diagnostics.Append(diags...)
	data.Containers = containersList
}

func (r *ContainerResource) flattenScalingToModel(ctx context.Context, scalingConfig *verda.ContainerScalingOptions, data *ContainerResourceModel, diagnostics *diag.Diagnostics) {
	if scalingConfig == nil || scalingConfig.ScaleDownPolicy == nil || scalingConfig.ScaleUpPolicy == nil ||
		scalingConfig.ScalingTriggers == nil || scalingConfig.ScalingTriggers.QueueLoad == nil {
		diagnostics.AddError("Incomplete scaling response", "Verda did not return the scale policies and queue trigger required by the container resource.")
		return
	}
	scaleDownPolicyObj, diags := types.ObjectValue(
		map[string]attr.Type{
			"delay_seconds": types.Int64Type,
		},
		map[string]attr.Value{
			"delay_seconds": types.Int64Value(int64(scalingConfig.ScaleDownPolicy.DelaySeconds)),
		},
	)
	diagnostics.Append(diags...)

	scaleUpPolicyObj, diags := types.ObjectValue(
		map[string]attr.Type{
			"delay_seconds": types.Int64Type,
		},
		map[string]attr.Value{
			"delay_seconds": types.Int64Value(int64(scalingConfig.ScaleUpPolicy.DelaySeconds)),
		},
	)
	diagnostics.Append(diags...)

	queueLoadObj, diags := types.ObjectValue(
		map[string]attr.Type{
			"threshold": types.Float64Type,
		},
		map[string]attr.Value{
			"threshold": types.Float64Value(scalingConfig.ScalingTriggers.QueueLoad.Threshold),
		},
	)
	diagnostics.Append(diags...)

	scalingObj, diags := types.ObjectValue(
		map[string]attr.Type{
			"min_replica_count":               types.Int64Type,
			"max_replica_count":               types.Int64Type,
			"queue_message_ttl_seconds":       types.Int64Type,
			"concurrent_requests_per_replica": types.Int64Type,
			"scale_down_policy": types.ObjectType{
				AttrTypes: map[string]attr.Type{
					"delay_seconds": types.Int64Type,
				},
			},
			"scale_up_policy": types.ObjectType{
				AttrTypes: map[string]attr.Type{
					"delay_seconds": types.Int64Type,
				},
			},
			"queue_load": types.ObjectType{
				AttrTypes: map[string]attr.Type{
					"threshold": types.Float64Type,
				},
			},
		},
		map[string]attr.Value{
			"min_replica_count":               types.Int64Value(int64(scalingConfig.MinReplicaCount)),
			"max_replica_count":               types.Int64Value(int64(scalingConfig.MaxReplicaCount)),
			"queue_message_ttl_seconds":       types.Int64Value(int64(scalingConfig.QueueMessageTTLSeconds)),
			"concurrent_requests_per_replica": types.Int64Value(int64(scalingConfig.ConcurrentRequestsPerReplica)),
			"scale_down_policy":               scaleDownPolicyObj,
			"scale_up_policy":                 scaleUpPolicyObj,
			"queue_load":                      queueLoadObj,
		},
	)
	diagnostics.Append(diags...)
	data.Scaling = scalingObj
}

// boolDefaultModifier is defined in resource_instance.go but we need it here too
// volumeMountValidator is now defined in validators.go and shared across resources
