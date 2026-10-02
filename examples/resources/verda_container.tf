# Basic container deployment example using public registry (no credentials needed)
# When container_registry_settings is omitted, it defaults to public registry
resource "verda_container" "example" {
  name = "my-web-app"

  compute = {
    name = "H100"
    size = 1
  }

  scaling = {
    min_replica_count               = 1
    max_replica_count               = 5
    queue_message_ttl_seconds       = 3600
    concurrent_requests_per_replica = 10

    scale_down_policy = {
      delay_seconds = 300
    }

    scale_up_policy = {
      delay_seconds = 300
    }

    queue_load = {
      threshold = 5.0
    }
  }

  containers = [
    {
      image        = "nginx:1.29.1"
      exposed_port = 80
    }
  ]
}

# Container deployment with private registry and environment variables
resource "verda_container_registry_credentials" "private" {
  name         = "private-registry-creds"
  type         = "dockerhub"
  username     = "privateuser"
  access_token = var.private_registry_token
}

resource "verda_container" "with_env" {
  name    = "api-service"
  is_spot = false

  compute = {
    name = "A100"
    size = 2
  }

  scaling = {
    min_replica_count               = 2
    max_replica_count               = 10
    queue_message_ttl_seconds       = 7200
    concurrent_requests_per_replica = 20

    scale_down_policy = {
      delay_seconds = 600
    }

    scale_up_policy = {
      delay_seconds = 150
    }

    queue_load = {
      threshold = 10.0
    }
  }

  container_registry_settings = {
    is_private  = "true"
    credentials = verda_container_registry_credentials.private.name
  }

  containers = [
    {
      image        = "myregistry.io/api:v1.0"
      exposed_port = 8080

      env = [
        {
          type                         = "plain"
          name                         = "PORT"
          value_or_reference_to_secret = "8080"
        },
        {
          type                         = "secret"
          name                         = "DATABASE_PASSWORD"
          value_or_reference_to_secret = "db-password-secret"
        }
      ]
    }
  ]
}

# Container deployment with configured healthcheck, entrypoint override, and volume mounts
resource "verda_container_registry_credentials" "healthcheck" {
  name         = "healthcheck-dockerhub"
  type         = "dockerhub"
  username     = "myuser"
  access_token = var.dockerhub_token
}

resource "verda_container" "with_healthcheck" {
  name    = "scalable-app"
  is_spot = true

  compute = {
    name = "H100"
    size = 1
  }

  scaling = {
    min_replica_count               = 1
    max_replica_count               = 10
    queue_message_ttl_seconds       = 1800
    concurrent_requests_per_replica = 15

    scale_down_policy = {
      delay_seconds = 450
    }

    scale_up_policy = {
      delay_seconds = 200
    }

    queue_load = {
      threshold = 8.0
    }
  }

  container_registry_settings = {
    is_private  = "true"
    credentials = verda_container_registry_credentials.healthcheck.name
  }

  containers = [
    {
      image        = "myapp:latest"
      exposed_port = 3000

      healthcheck = {
        enabled = "true"
        port    = "3000"
        path    = "/health"
      }

      # Override the container entrypoint to run a custom command
      entrypoint_overrides = {
        enabled    = true
        entrypoint = ["/bin/sh", "-c"]
        cmd        = ["python app.py --port 3000"]
      }

      # Mount volumes into the container
      volume_mounts = [
        # Scratch volume with optional size
        {
          type       = "scratch"
          mount_path = "/tmp/cache"
          size_in_mb = 1024
        },
        # Memory volume (tmpfs) with optional size
        {
          type       = "memory"
          mount_path = "/dev/shm"
          size_in_mb = 2048
        },
        # Secret volume (requires secret_name)
        {
          type        = "secret"
          mount_path  = "/secrets/config"
          secret_name = "app-config-secret"
        }
      ]
    }
  ]
}

# Container deployment with private registry using GCR
resource "verda_container_registry_credentials" "gcr" {
  name                = "gcr-credentials"
  type                = "gcr"
  service_account_key = file("${path.module}/gcr-key.json")
}

resource "verda_container" "with_private_registry" {
  name = "private-app"

  compute = {
    name = "A100"
    size = 1
  }

  scaling = {
    min_replica_count               = 1
    max_replica_count               = 3
    queue_message_ttl_seconds       = 3600
    concurrent_requests_per_replica = 5

    scale_down_policy = {
      delay_seconds = 300
    }

    scale_up_policy = {
      delay_seconds = 300
    }

    queue_load = {
      threshold = 3.0
    }
  }

  container_registry_settings = {
    is_private  = "true"
    credentials = verda_container_registry_credentials.gcr.name
  }

  containers = [
    {
      image        = "gcr.io/my-project/app:latest"
      exposed_port = 8080
    }
  ]
}

# Container deployment with shared volume mount
resource "verda_volume" "data_volume" {
  name     = "shared-data-volume"
  size     = 100
  type     = "NVMe_Shared"
  location = "FIN-01"
}

resource "verda_container" "with_shared_volume" {
  name = "data-processor"

  compute = {
    name = "H100"
    size = 1
  }

  scaling = {
    min_replica_count               = 1
    max_replica_count               = 5
    queue_message_ttl_seconds       = 3600
    concurrent_requests_per_replica = 10

    scale_down_policy = {
      delay_seconds = 300
    }

    scale_up_policy = {
      delay_seconds = 300
    }

    queue_load = {
      threshold = 5.0
    }
  }

  containers = [
    {
      image        = "data-processor:latest"
      exposed_port = 8000

      volume_mounts = [
        # Shared volume (requires volume_id)
        {
          type       = "shared"
          mount_path = "/data"
          volume_id  = verda_volume.data_volume.id
        }
      ]
    }
  ]
}
